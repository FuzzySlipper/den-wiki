package reader

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Topic is one node of the subject tree, from taxonomy.yml's topics.
type Topic struct {
	Name        string
	Description string
	Parent      *Topic
	Children    []*Topic
	// Overview is the page that introduces the topic (taxonomy.yml's
	// overview): the tree shows the topic as that page, with the topic's
	// subtopics and other pages under it. Pages does not list it again.
	Overview *Entry
	// Pages are the pages that name this topic: hubs first, then by title.
	Pages []*Entry
	// Total counts the pages here and in every subtopic.
	Total int
}

// Title is the topic's display name: its overview page's title, or the
// topic name as a title.
func (t *Topic) Title() string {
	if t.Overview != nil {
		return t.Overview.Title
	}
	return titleOf(t.Name)
}

// URL is where the topic's name links: its overview page, or its listing.
func (t *Topic) URL() string {
	if t.Overview != nil {
		return t.Overview.URL
	}
	return "/topic/" + t.Name
}

// Ancestors returns the topic's parents, outermost first.
func (t *Topic) Ancestors() []*Topic {
	var out []*Topic
	for p := t.Parent; p != nil; p = p.Parent {
		out = append([]*Topic{p}, out...)
	}
	return out
}

// Entry is a wiki page as listings show it.
type Entry struct {
	Path     string
	URL      string
	Title    string
	Type     string
	Topic    string
	Lede     string
	Updated  string
	Curation string
}

// Library is the wiki as the reader lists it: the topic tree, the pages no
// topic holds, and the recently updated pages.
type Library struct {
	Topics   []*Topic
	ByName   map[string]*Topic
	Unsorted []*Entry
	Pages    map[string]*Entry
	// Recent are the most recently updated pages.
	Recent     []*Entry
	Unreviewed int
	Count      int
}

// site holds the content root and caches what it renders.
type site struct {
	root *os.Root
	// ttl bounds how often the library re-walks wiki/ to see whether
	// anything changed.
	ttl time.Duration

	mu      sync.Mutex
	docs    map[string]cachedDoc
	lib     *Library
	libSum  string
	libTime time.Time
}

type cachedDoc struct {
	mod  time.Time
	size int64
	doc  *Doc
}

// doc renders rel, or returns the cached render while the file is unchanged.
func (s *site) doc(rel string) (*Doc, error) {
	st, err := s.root.Stat(rel)
	if err != nil || st.IsDir() {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, rel)
	}
	s.mu.Lock()
	c, ok := s.docs[rel]
	s.mu.Unlock()
	// A render also depends on the files it links to and cites: a link turns
	// red or blue, and References show each source's URL.
	if ok && c.mod.Equal(st.ModTime()) && c.size == st.Size() && c.doc.fresh(s.root) {
		return c.doc, nil
	}
	d, err := Render(s.root, rel)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.docs[rel] = cachedDoc{mod: st.ModTime(), size: st.Size(), doc: d}
	s.mu.Unlock()
	return d, nil
}

// library returns the topic tree, rebuilt when taxonomy.yml or a page
// changed.
func (s *site) library() (*Library, error) {
	s.mu.Lock()
	if s.lib != nil && time.Since(s.libTime) < s.ttl {
		lib := s.lib
		s.mu.Unlock()
		return lib, nil
	}
	s.mu.Unlock()
	pages, sum, err := s.scan()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.lib != nil && sum == s.libSum {
		s.libTime = time.Now()
		lib := s.lib
		s.mu.Unlock()
		return lib, nil
	}
	s.mu.Unlock()
	// Building renders pages through doc, which takes the lock itself.
	lib, err := s.build(pages)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.lib, s.libSum, s.libTime = lib, sum, time.Now()
	s.mu.Unlock()
	return lib, nil
}

// scan lists the wiki pages and fingerprints them with taxonomy.yml.
func (s *site) scan() ([]string, string, error) {
	h := sha256.New()
	if st, err := s.root.Stat(taxonomyFile); err == nil {
		fmt.Fprintf(h, "%s %d %d\n", taxonomyFile, st.ModTime().UnixNano(), st.Size())
	}
	var pages []string
	err := fs.WalkDir(s.root.FS(), "wiki", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == "wiki" {
				return fs.SkipAll
			}
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p != "wiki" && (strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".assets")) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".md") || name == "index.md" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		fmt.Fprintf(h, "%s %d %d\n", p, info.ModTime().UnixNano(), info.Size())
		pages = append(pages, p)
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return pages, fmt.Sprintf("%x", h.Sum(nil)), nil
}

const taxonomyFile = "taxonomy.yml"

type taxonomyTopics struct {
	Topics []struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		Parent      string `yaml:"parent"`
		Overview    string `yaml:"overview"`
	} `yaml:"topics"`
}

func (s *site) build(pages []string) (*Library, error) {
	lib := &Library{ByName: map[string]*Topic{}, Pages: map[string]*Entry{}}
	var tax taxonomyTopics
	if src, err := s.root.ReadFile(taxonomyFile); err == nil {
		// A broken taxonomy is a lint finding; the reader shows every page
		// as unsorted rather than failing.
		_ = yaml.Unmarshal(src, &tax)
	}
	for _, t := range tax.Topics {
		if t.Name != "" && lib.ByName[t.Name] == nil {
			lib.ByName[t.Name] = &Topic{Name: t.Name, Description: t.Description}
		}
	}
	for _, t := range tax.Topics {
		node := lib.ByName[t.Name]
		if node == nil || node.Parent != nil {
			continue
		}
		if p := lib.ByName[t.Parent]; p != nil && !descends(p, node) {
			node.Parent = p
			p.Children = append(p.Children, node)
		}
	}
	for _, t := range tax.Topics {
		if node := lib.ByName[t.Name]; node != nil && node.Parent == nil && !contains(lib.Topics, node) {
			lib.Topics = append(lib.Topics, node)
		}
	}
	for _, rel := range pages {
		d, err := s.doc(rel)
		if err != nil {
			continue
		}
		e := &Entry{
			Path: rel, URL: URL(rel), Title: d.Title, Type: d.Fields.Str("type"), Topic: d.Fields.Str("topic"),
			Lede: d.Lede, Updated: d.Fields.Str("updated"), Curation: d.Fields.Str("curation"),
		}
		lib.Pages[rel] = e
		lib.Count++
		if e.Curation != "curated" {
			lib.Unreviewed++
		}
		if t := lib.ByName[e.Topic]; t != nil {
			t.Pages = append(t.Pages, e)
			for ; t != nil; t = t.Parent {
				t.Total++
			}
		} else {
			lib.Unsorted = append(lib.Unsorted, e)
		}
		lib.Recent = append(lib.Recent, e)
	}
	// An overview must be a wiki page this scan found; anything else
	// (lint's taxonomy.topic_overview) leaves the topic plain. A page
	// introduces one topic: the first that names it.
	claimed := map[string]bool{}
	for _, t := range tax.Topics {
		node, e := lib.ByName[t.Name], lib.Pages[t.Overview]
		if node == nil || node.Overview != nil || e == nil || claimed[e.Path] {
			continue
		}
		claimed[e.Path] = true
		node.Overview = e
		// The overview is the topic's entry, so it is not listed again
		// wherever its own topic field would put it.
		if home := lib.ByName[e.Topic]; home != nil {
			home.Pages = without(home.Pages, e)
		} else {
			lib.Unsorted = without(lib.Unsorted, e)
		}
	}
	for _, t := range lib.ByName {
		sortEntries(t.Pages)
	}
	sortEntries(lib.Unsorted)
	sort.SliceStable(lib.Recent, func(i, j int) bool { return lib.Recent[i].Updated > lib.Recent[j].Updated })
	if len(lib.Recent) > 10 {
		lib.Recent = lib.Recent[:10]
	}
	return lib, nil
}

// descends reports whether t is node or sits below it, which would make
// node's parent a cycle. Lint reports cycles; the reader breaks them.
func descends(t, node *Topic) bool {
	for ; t != nil; t = t.Parent {
		if t == node {
			return true
		}
	}
	return false
}

func without(es []*Entry, e *Entry) []*Entry {
	out := es[:0:0]
	for _, x := range es {
		if x != e {
			out = append(out, x)
		}
	}
	return out
}

func contains(ts []*Topic, t *Topic) bool {
	for _, x := range ts {
		if x == t {
			return true
		}
	}
	return false
}

func sortEntries(es []*Entry) {
	sort.SliceStable(es, func(i, j int) bool {
		if hi, hj := es[i].Type == "hub", es[j].Type == "hub"; hi != hj {
			return hi
		}
		return strings.ToLower(es[i].Title) < strings.ToLower(es[j].Title)
	})
}
