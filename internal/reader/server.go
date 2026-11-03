package reader

import (
	"context"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/FuzzySlipper/den-wiki/internal/content"
	"github.com/FuzzySlipper/den-wiki/internal/media"
)

// Backend is what the reader needs from OpenKnowledge: a page's backlinks
// and history, and search. The facade implements it over OK; while OK is
// down the reader still shows pages, without these panels.
type Backend interface {
	Briefing(ctx context.Context, rel string) (Briefing, error)
	Search(ctx context.Context, query string, limit int) ([]Hit, error)
}

// Briefing is OK's view of one page.
type Briefing struct {
	// Backlinks are the content files that link to the page.
	Backlinks []Backlink
	// History is the page's git history, newest first.
	History []Change
}

// Backlink is one file linking to a page.
type Backlink struct {
	Path  string
	Title string
}

// Change is one commit that changed a page.
type Change struct {
	Date    string
	Author  string
	Subject string
	Commit  string
	// Trail is the (trail:<slug>) the subject names, if any.
	Trail string
}

// Hit is one search result.
type Hit struct {
	Path    string
	Title   string
	Snippet string
}

// Config configures the reader.
type Config struct {
	// ContentDir is the den-wiki-content checkout.
	ContentDir string
	// SiteTitle names the wiki in the header; default "den-wiki".
	SiteTitle string
	// EditURL is the base URL of OpenKnowledge's editor; a page's "Edit in
	// OK" link is EditURL + "/#/" + its document name. Empty hides the link.
	EditURL string
	// Backend is OK for backlinks, history and search; nil hides them.
	Backend Backend
	Logger  *slog.Logger
}

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// briefingTTL is how long a page's backlinks and history are reused.
const briefingTTL = time.Minute

// Server is the reader's HTTP handler.
type Server struct {
	cfg  Config
	site *site
	tmpl *template.Template
	mux  *http.ServeMux

	mu        sync.Mutex
	briefings map[string]cachedBriefing
}

type cachedBriefing struct {
	at time.Time
	b  Briefing
}

// New opens the content dir and returns the reader. Close releases it.
func New(cfg Config) (*Server, error) {
	if cfg.SiteTitle == "" {
		cfg.SiteTitle = "den-wiki"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	cfg.EditURL = strings.TrimSuffix(cfg.EditURL, "/")
	root, err := os.OpenRoot(cfg.ContentDir)
	if err != nil {
		return nil, err
	}
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"url":      URL,
		"titleOf":  titleOf,
		"treeArgs": newTreeArgs,
	}).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		root.Close()
		return nil, err
	}
	s := &Server{
		cfg:       cfg,
		site:      &site{root: root, ttl: 2 * time.Second, docs: map[string]cachedDoc{}},
		tmpl:      tmpl,
		mux:       http.NewServeMux(),
		briefings: map[string]cachedBriefing{},
	}
	static, _ := fs.Sub(staticFS, "static")
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", staticHandler(http.FileServerFS(static))))
	s.mux.HandleFunc("GET /{$}", s.home)
	s.mux.HandleFunc("GET /topic/{name}", s.topic)
	s.mux.HandleFunc("GET /unsorted", s.unsorted)
	s.mux.HandleFunc("GET /search", s.search)
	s.mux.HandleFunc("GET /wiki/{path...}", s.file)
	s.mux.HandleFunc("GET /sources/{path...}", s.file)
	s.mux.HandleFunc("GET /trails/{path...}", s.file)
	return s, nil
}

// Close releases the content dir.
func (s *Server) Close() error { return s.site.root.Close() }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: https:; media-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'self'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	s.mux.ServeHTTP(w, r)
}

func staticHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		h.ServeHTTP(w, r)
	})
}

// view is what every page template gets.
type view struct {
	Site    string
	Title   string
	Lib     *Library
	Current string // the open topic or page path, for the tree
	Query   string
	Edit    string
	// One of these is set, by which template renders the main pane.
	Doc     *docView
	Topic   *Topic
	Home    bool
	Search  *searchView
	Missing string
	Status  int
}

type docView struct {
	*Doc
	Zone      string
	TopicNode *Topic
	Tags      []string
	Aliases   []string
	Trails    []string
	Conflicts []string
	Briefing  *Briefing
	// Unavailable says why backlinks and history are missing.
	Unavailable string
	SourceURL   string
}

type searchView struct {
	Results     []Hit
	Unavailable string
}

func (s *Server) render(w http.ResponseWriter, v *view) {
	lib, err := s.site.library()
	if err != nil {
		s.cfg.Logger.Error("reader_library", "error", err.Error())
		lib = &Library{ByName: map[string]*Topic{}}
	}
	v.Site, v.Lib = s.cfg.SiteTitle, lib
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if v.Status != 0 {
		w.WriteHeader(v.Status)
	}
	if err := s.tmpl.ExecuteTemplate(w, "layout.html", v); err != nil {
		s.cfg.Logger.Error("reader_template", "error", err.Error())
	}
}

func (s *Server) home(w http.ResponseWriter, _ *http.Request) {
	s.render(w, &view{Title: s.cfg.SiteTitle, Home: true})
}

func (s *Server) topic(w http.ResponseWriter, r *http.Request) {
	lib, err := s.site.library()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	t := lib.ByName[r.PathValue("name")]
	if t == nil {
		s.render(w, &view{Title: "No such topic", Missing: "topic " + r.PathValue("name"), Status: http.StatusNotFound})
		return
	}
	s.render(w, &view{Title: titleOf(t.Name), Topic: t, Current: "topic:" + t.Name})
}

func (s *Server) unsorted(w http.ResponseWriter, _ *http.Request) {
	lib, err := s.site.library()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	t := &Topic{Name: "unsorted", Description: "Pages with no topic yet. The librarian sorts them.", Pages: lib.Unsorted, Total: len(lib.Unsorted)}
	s.render(w, &view{Title: "Unsorted", Topic: t, Current: "unsorted"})
}

func titleOf(name string) string {
	if name == "" {
		return name
	}
	return strings.ToUpper(name[:1]) + strings.ReplaceAll(name[1:], "-", " ")
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	v := &view{Title: "Search", Query: q, Search: &searchView{}}
	if q != "" {
		if s.cfg.Backend == nil {
			v.Search.Unavailable = "Search needs OpenKnowledge, which this den-wiki is not configured with."
		} else {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			hits, err := s.cfg.Backend.Search(ctx, q, 50)
			if err != nil {
				s.cfg.Logger.Warn("reader_search", "error", err.Error())
				v.Search.Unavailable = "Search is unavailable: OpenKnowledge did not answer."
			}
			v.Search.Results = rankHits(hits)
		}
		v.Title = "Search: " + q
	}
	s.render(w, v)
}

// rankHits puts wiki pages first, keeping OK's order within each zone.
func rankHits(hits []Hit) []Hit {
	var wiki, rest []Hit
	for _, h := range hits {
		if strings.HasPrefix(h.Snippet, "---") {
			h.Snippet = "" // OK's snippet of a frontmatter block
		}
		if zoneOf(h.Path) == "wiki" {
			wiki = append(wiki, h)
		} else {
			rest = append(rest, h)
		}
	}
	return append(wiki, rest...)
}

// zoneOf is the zone a content path is in.
func zoneOf(rel string) string {
	zone, _, _ := strings.Cut(rel, "/")
	return zone
}

// file serves a page, source or trail as HTML, or a file one shows.
func (s *Server) file(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if !content.Clean(rel) || strings.Contains("/"+rel+"/", "/.") {
		http.NotFound(w, r)
		return
	}
	if t, ok := media.TypeOf(rel); ok {
		s.asset(w, r, rel, t)
		return
	}
	if strings.HasSuffix(rel, ".md") {
		http.Redirect(w, r, URL(rel), http.StatusMovedPermanently)
		return
	}
	d, err := s.site.doc(rel + ".md")
	if errors.Is(err, ErrNotFound) {
		s.render(w, &view{Title: "No such page", Missing: rel, Status: http.StatusNotFound, Edit: s.editURL(rel)})
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, &view{Title: d.Title, Doc: s.docView(r.Context(), d), Current: d.Path, Edit: s.editURL(rel)})
}

func (s *Server) editURL(doc string) string {
	if s.cfg.EditURL == "" {
		return ""
	}
	return s.cfg.EditURL + "/#/" + doc
}

func (s *Server) docView(ctx context.Context, d *Doc) *docView {
	v := &docView{
		Doc: d, Zone: zoneOf(d.Path),
		Tags: d.Fields.List("tags"), Aliases: d.Fields.List("aliases"),
		Trails: d.Fields.List("trails"), Conflicts: d.Fields.List("contradicts"),
	}
	if v.Zone == "sources" {
		v.SourceURL = content.SourceURL(s.site.root, d.Path)
	}
	if lib, err := s.site.library(); err == nil {
		v.TopicNode = lib.ByName[d.Fields.Str("topic")]
	}
	if s.cfg.Backend == nil {
		v.Unavailable = "Backlinks and history need OpenKnowledge, which this den-wiki is not configured with."
		return v
	}
	b, err := s.briefing(ctx, d.Path)
	if err != nil {
		s.cfg.Logger.Warn("reader_briefing", "path", d.Path, "error", err.Error())
		v.Unavailable = "Backlinks and history are unavailable: OpenKnowledge did not answer."
		return v
	}
	v.Briefing = &b
	return v
}

func (s *Server) briefing(ctx context.Context, rel string) (Briefing, error) {
	s.mu.Lock()
	c, ok := s.briefings[rel]
	s.mu.Unlock()
	if ok && time.Since(c.at) < briefingTTL {
		return c.b, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b, err := s.cfg.Backend.Briefing(ctx, rel)
	if err != nil {
		return Briefing{}, err
	}
	s.mu.Lock()
	s.briefings[rel] = cachedBriefing{at: time.Now(), b: b}
	s.mu.Unlock()
	return b, nil
}

// TrailRe finds the trail a commit subject names.
var TrailRe = regexp.MustCompile(`\(trail:([a-z0-9]+(?:-[a-z0-9]+)*)\)`)

// asset serves a file a page shows, from the content dir, sandboxed like
// the media store so an SVG opened on its own runs no script.
func (s *Server) asset(w http.ResponseWriter, r *http.Request, rel string, t media.Type) {
	f, err := s.site.root.Open(rel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", t.MIME)
	h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; sandbox")
	h.Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, path.Base(rel), st.ModTime(), f)
}

// treeArgs is one node of the topic tree as the template draws it.
type treeArgs struct {
	Topic   *Topic
	Current string
	// Here is the open topic; Open shows the node expanded because the open
	// topic or page is in it.
	Here, Open bool
}

func newTreeArgs(t *Topic, current string) treeArgs {
	a := treeArgs{Topic: t, Current: current, Here: current == "topic:"+t.Name}
	a.Open = a.Here || t.holds(current)
	return a
}

// holds reports whether the open topic or page is in t's subtree.
func (t *Topic) holds(current string) bool {
	for _, e := range t.Pages {
		if e.Path == current {
			return true
		}
	}
	for _, c := range t.Children {
		if current == "topic:"+c.Name || c.holds(current) {
			return true
		}
	}
	return false
}
