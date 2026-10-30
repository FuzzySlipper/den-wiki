package facade

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/FuzzySlipper/den-wiki/internal/okclient"
	"github.com/FuzzySlipper/den-wiki/internal/registry"
)

// WikiBackend is OpenKnowledge as the facade uses it. *okclient.Client
// implements it.
type WikiBackend interface {
	Ping(ctx context.Context) error
	Call(ctx context.Context, identity, tool string, args any) (okclient.CallResult, error)
}

// NewOKWiki adapts an OpenKnowledge client. Each agent gets its own OK
// session, initialised with clientInfo.name = den-wiki/<agent> and a stable
// x-ok-connection-id, because OK attributes writes per session (#10462).
func NewOKWiki(c *okclient.Client) WikiBackend {
	c.Identify = func(identity string, h http.Header) {
		if identity != "" {
			h.Set("x-ok-connection-id", "den-wiki-"+strings.Trim(nonIdent.ReplaceAllString(strings.ToLower(identity), "-"), "-."))
		}
	}
	return c
}

var (
	// Paths the facade passes to OK's exec must be plain: no spaces or shell
	// characters. Lint already flags anything else in wiki/.
	safePath = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
	zones    = []string{"sources", "trails", "wiki"}
)

func badArg(format string, args ...any) error {
	return &registry.Error{Code: registry.CodeInvalidArguments, Message: fmt.Sprintf(format, args...)}
}

// docPath checks a content-root-relative Markdown path in a zone and returns
// it with OK's extension-less doc name.
func docPath(p string) (rel, doc string, err error) {
	p = strings.TrimPrefix(strings.TrimSpace(p), "/")
	if !strings.HasSuffix(p, ".md") {
		return "", "", badArg("path %q must name a .md file, e.g. wiki/agent-memory.md", p)
	}
	if err := checkContentPath(p); err != nil {
		return "", "", err
	}
	if path.Base(p) == "index.md" {
		return "", "", badArg("path %q is a generated index; OpenKnowledge maintains it", p)
	}
	return p, strings.TrimSuffix(p, ".md"), nil
}

func checkContentPath(p string) error {
	if !safePath.MatchString(p) || path.Clean(p) != p {
		return badArg("path %q may only use letters, digits, '.', '_', '-' and '/', with no '..' or doubled slashes", p)
	}
	segs := strings.Split(p, "/")
	ok := false
	for _, z := range zones {
		if segs[0] == z {
			ok = true
		}
	}
	if !ok {
		return badArg("path %q is outside the zones; it must start with sources/, trails/ or wiki/", p)
	}
	for _, s := range segs {
		if strings.HasPrefix(s, ".") {
			return badArg("path %q has a hidden segment; .ok/ and other dot-folders are tooling, not content", p)
		}
	}
	return nil
}

var trailSlug = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func checkTrail(t string) error {
	if !trailSlug.MatchString(t) {
		return badArg("trail %q must be the slug of the run's trail page (trails/<slug>.md)", t)
	}
	return nil
}

// okError maps OK client errors onto registry codes.
func okError(err error) error {
	var te *okclient.ToolError
	switch {
	case errors.Is(err, okclient.ErrUnavailable):
		return &registry.Error{Code: registry.CodeBackendUnavailable, Retryable: true, Message: err.Error()}
	case errors.As(err, &te):
		msg := te.Message
		if strings.Contains(strings.ToLower(msg), "not found") || strings.Contains(strings.ToLower(msg), "does not exist") {
			return &registry.Error{Code: registry.CodeNotFound, Message: "openknowledge: " + msg}
		}
		return &registry.Error{Code: registry.CodeBackendError, Message: "openknowledge: " + msg}
	case err != nil:
		return &registry.Error{Code: registry.CodeBackendError, Message: err.Error()}
	}
	return nil
}

// wiki bundles what the wiki operations need.
type wiki struct {
	ok   WikiBackend
	dir  string
	git  *committer
	deps bool
}

func (w *wiki) ready() error {
	if !w.deps {
		return &registry.Error{Code: registry.CodeBackendUnavailable, Message: "this den-wiki has no OpenKnowledge or content_dir configured, so wiki tools are unavailable"}
	}
	return nil
}

func (w *wiki) call(ctx context.Context, agent, tool string, args map[string]any) (map[string]any, error) {
	res, err := w.ok.Call(ctx, agent, tool, args)
	if err != nil {
		return nil, okError(err)
	}
	var out map[string]any
	if len(res.Structured) > 0 {
		dec := json.NewDecoder(bytes.NewReader(res.Structured))
		dec.UseNumber()
		if err := dec.Decode(&out); err != nil {
			return nil, &registry.Error{Code: registry.CodeBackendError, Message: "openknowledge " + tool + " returned non-object structured content"}
		}
	}
	if out == nil {
		out = map[string]any{"text": res.Text}
	}
	return out, nil
}

// drop removes OK bookkeeping fields the facade does not pass on.
func drop(m map[string]any, keys ...string) map[string]any {
	for _, k := range append(keys, "cwd", "previewUrl") {
		delete(m, k)
	}
	return m
}

// zoneRules reads a zone's .ok/frontmatter.yml.
func (w *wiki) zoneRules(rel string) map[string]any {
	zone, _, _ := strings.Cut(rel, "/")
	raw, err := os.ReadFile(w.git.abs(zone + "/.ok/frontmatter.yml"))
	if err != nil {
		return map[string]any{"zone": zone}
	}
	var m map[string]any
	if yaml.Unmarshal(raw, &m) != nil || m == nil {
		return map[string]any{"zone": zone}
	}
	return m
}

// noise reports link-graph entries that are OK artefacts rather than content:
// templates and tooling under dot-folders, generated indexes, and OK's
// misparse of one-word footnote link text ("[Word](path)" as a target).
func noise(doc string) bool {
	if strings.Contains(doc, "](") || strings.HasPrefix(doc, "[") {
		return true
	}
	for _, s := range strings.Split(doc, "/") {
		if strings.HasPrefix(s, ".") {
			return true
		}
	}
	return path.Base(doc) == "index"
}

// pagePaths collects, in order and once each, the content documents a read
// response names under path, docName or source, as .md paths. Generated
// indexes, templates and documents that do not exist (dead link targets) are
// left out, so the read log holds only real pages the caller was shown.
func (w *wiki) pagePaths(v any) []string {
	seen := map[string]bool{}
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			for _, k := range []string{"path", "docName", "source"} {
				s, _ := t[k].(string)
				rel := strings.TrimSuffix(s, ".md") + ".md"
				if s == "" || seen[rel] || noise(strings.TrimSuffix(rel, ".md")) || checkContentPath(rel) != nil || !w.git.exists(rel) {
					continue
				}
				seen[rel] = true
				out = append(out, rel)
			}
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(t[k])
			}
		}
	}
	walk(v)
	return out
}

func filterList(v any, _ string, keep func(map[string]any) bool) []any {
	items, _ := v.([]any)
	out := []any{}
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if keep(m) {
			delete(m, "previewUrl")
			out = append(out, m)
		}
	}
	return out
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

type wikiReadArgs struct {
	Path string `json:"path" required:"true" desc:"Content-root-relative page, source or trail, e.g. wiki/agent-memory.md."`
}

type wikiListArgs struct {
	Folder string `json:"folder" required:"true" desc:"Folder to list, e.g. wiki or wiki/ai."`
}

type wikiSearchArgs struct {
	Query      string `json:"query" required:"true" desc:"Title, path or body terms."`
	TitlesOnly bool   `json:"titles_only,omitempty" desc:"Match titles, paths and folders only (faster)."`
	Limit      int    `json:"limit,omitempty" min:"1" max:"100" desc:"Default 20."`
}

type wikiLinksArgs struct {
	Kinds []string `json:"kinds" required:"true" desc:"Any of backlinks, forward, dead, orphans, hubs, suggest."`
	Path  string   `json:"path,omitempty" desc:"Page for backlinks, forward and suggest, e.g. wiki/agent-memory.md."`
	Limit int      `json:"limit,omitempty" min:"1" max:"100" desc:"Maximum hubs; default 20."`
}

type wikiHistoryArgs struct {
	Path   string `json:"path" required:"true" desc:"Page, source or trail."`
	Limit  int    `json:"limit,omitempty" min:"1" max:"200" desc:"Default 50."`
	Offset int    `json:"offset,omitempty" min:"0"`
	Author string `json:"author,omitempty" desc:"Only entries by this author."`
}

type wikiAuditArgs struct {
	Path string `json:"path,omitempty" desc:"Folder or document to audit; default the whole content repo."`
}

type wikiWriteArgs struct {
	Path    string `json:"path" required:"true" desc:"Content-root-relative .md path to create or replace."`
	Content string `json:"content" required:"true" desc:"The whole file, frontmatter block included."`
	Trail   string `json:"trail" required:"true" desc:"Slug of your run's trail page; it goes in the commit subject as (trail:<slug>)."`
	Summary string `json:"summary,omitempty" desc:"One line saying what changed; default \"Write <path>\"."`
}

type wikiEditArgs struct {
	Path        string         `json:"path" required:"true" desc:"Page, source or trail to edit."`
	Find        *string        `json:"find,omitempty" desc:"Exact body text to replace. Give find and replace, or frontmatter."`
	Replace     *string        `json:"replace,omitempty"`
	Occurrence  int            `json:"occurrence,omitempty" min:"1" desc:"Which match of find (default 1)."`
	Frontmatter map[string]any `json:"frontmatter,omitempty" desc:"Merge-patch of frontmatter keys; null deletes a key."`
	Trail       string         `json:"trail" required:"true" desc:"Slug of your run's trail page."`
	Summary     string         `json:"summary,omitempty" desc:"One line saying what changed."`
}

type wikiMoveArgs struct {
	From    string `json:"from" required:"true" desc:"Current .md path."`
	To      string `json:"to" required:"true" desc:"New .md path. Inline links to it are rewritten."`
	Trail   string `json:"trail" required:"true" desc:"Slug of your run's trail page."`
	Summary string `json:"summary,omitempty"`
}

type wikiDeleteArgs struct {
	Path    string `json:"path" required:"true" desc:"Page to delete. Inbound links become dead links; merge into a successor first."`
	Trail   string `json:"trail" required:"true" desc:"Slug of the trail recording why."`
	Summary string `json:"summary,omitempty"`
}

func subject(summary, fallback, trail string) string {
	s := strings.Join(strings.Fields(summary), " ")
	if s == "" {
		s = fallback
	}
	return s + " (trail:" + trail + ")"
}

// writeResult shapes a write's result: OK's outcome and warnings, and the
// commit den-wiki made (or why it made none).
func writeResult(rel string, ok map[string]any, c *Commit, cerr error) map[string]any {
	out := map[string]any{"path": rel, "commit": c}
	if doc, _ := ok["document"].(map[string]any); doc != nil {
		out["warnings"] = doc["warnings"]
		out["broken_links"] = doc["brokenLinks"]
	}
	if v, ok := ok["outcome"]; ok {
		out["outcome"] = v
	}
	if cerr != nil {
		out["commit_error"] = cerr.Error()
	}
	return out
}

func wikiOps(ok WikiBackend, contentDir string) []*registry.Operation {
	w := &wiki{ok: ok, dir: contentDir, git: &committer{dir: contentDir}, deps: ok != nil && contentDir != ""}
	const backend = "openknowledge"
	return []*registry.Operation{
		registry.New(registry.Operation{
			Name: "wiki_read", Risk: registry.RiskRead, Tier: registry.TierDefault, Backend: backend,
			Description: "Read a page, source or trail: its content, frontmatter, backlinks, outgoing links, edit history, git history (with trail-tagged subjects) and its zone's rules.",
			Examples:    []registry.Example{{Description: "Read a page with its links", Arguments: map[string]any{"path": "wiki/agent-memory.md"}}},
		}, func(ctx context.Context, c *registry.Call, a wikiReadArgs) (any, error) {
			if err := w.ready(); err != nil {
				return nil, err
			}
			rel, _, err := docPath(a.Path)
			if err != nil {
				return nil, err
			}
			content, found := w.git.read(rel)
			if !found {
				return nil, &registry.Error{Code: registry.CodeNotFound, Message: rel + " does not exist; wiki_search finds pages"}
			}
			res, err := w.call(ctx, c.Agent, "exec", map[string]any{"command": "cat " + rel})
			if err != nil {
				return nil, err
			}
			out := map[string]any{"path": rel, "content": string(content), "zone": w.zoneRules(rel)}
			if eps, _ := res["enrichedPaths"].([]any); len(eps) > 0 {
				ep, _ := eps[0].(map[string]any)
				for _, k := range []string{"title", "tags", "frontmatter", "history", "projectHistory", "graphRole"} {
					out[snake(k)] = ep[k]
				}
				out["backlinks"] = filterList(ep["backlinks"], "", func(m map[string]any) bool { return !noise(str(m, "source")) })
				out["forward_links"] = filterList(ep["forwardLinks"], "", func(m map[string]any) bool { return !noise(str(m, "docName")) })
			}
			c.NoteRead(rel, nil, []string{rel})
			return out, nil
		}),
		registry.New(registry.Operation{
			Name: "wiki_list", Risk: registry.RiskRead, Backend: backend,
			Description: "List a folder: its documents and subfolders, its templates, and its zone's rules.",
		}, func(ctx context.Context, c *registry.Call, a wikiListArgs) (any, error) {
			if err := w.ready(); err != nil {
				return nil, err
			}
			folder := strings.Trim(strings.TrimSpace(a.Folder), "/")
			if err := checkContentPath(folder); err != nil {
				return nil, err
			}
			res, err := w.call(ctx, c.Agent, "exec", map[string]any{"command": "ls " + folder})
			if err != nil {
				return nil, err
			}
			out := drop(res, "text")
			out["folder"] = folder
			out["zone"] = w.zoneRules(folder + "/")
			c.NoteRead(folder, w.pagePaths(out["enrichedPaths"]), nil)
			return out, nil
		}),
		registry.New(registry.Operation{
			Name: "wiki_search", Risk: registry.RiskRead, Tier: registry.TierDefault, Backend: backend,
			Description: "Ranked search over titles, paths and page bodies. Search before writing: one concept, one page.",
			Examples:    []registry.Example{{Description: "Find pages about agent memory", Arguments: map[string]any{"query": "agent memory", "limit": 10}}},
		}, func(ctx context.Context, c *registry.Call, a wikiSearchArgs) (any, error) {
			if err := w.ready(); err != nil {
				return nil, err
			}
			args := map[string]any{"query": a.Query}
			if a.TitlesOnly {
				args["intent"] = "omnibar"
			}
			if a.Limit > 0 {
				args["limit"] = a.Limit
			}
			res, err := w.call(ctx, c.Agent, "search", args)
			if err != nil {
				return nil, err
			}
			var returned []string
			results := filterList(res["results"], "", func(m map[string]any) bool {
				doc := str(m, "docName")
				if doc == "" {
					doc = str(m, "path")
				}
				if noise(doc) {
					return false
				}
				if str(m, "kind") == "page" {
					m["path"] = doc + ".md"
					returned = append(returned, doc+".md")
				}
				delete(m, "docName")
				return true
			})
			c.NoteRead(a.Query, returned, nil)
			return map[string]any{"query": a.Query, "results": results}, nil
		}),
		registry.New(registry.Operation{
			Name: "wiki_links", Risk: registry.RiskRead, Backend: backend,
			Description: "Link-graph views: backlinks, forward, dead, orphans, hubs, suggest. Templates, tooling and generated indexes are filtered out.",
		}, func(ctx context.Context, c *registry.Call, a wikiLinksArgs) (any, error) {
			if err := w.ready(); err != nil {
				return nil, err
			}
			valid := map[string]bool{"backlinks": true, "forward": true, "dead": true, "orphans": true, "hubs": true, "suggest": true}
			if len(a.Kinds) == 0 {
				return nil, badArg("kinds needs at least one of backlinks, forward, dead, orphans, hubs, suggest")
			}
			for _, k := range a.Kinds {
				if !valid[k] {
					return nil, badArg("unknown link kind %q (want backlinks, forward, dead, orphans, hubs or suggest)", k)
				}
			}
			args := map[string]any{"kind": a.Kinds}
			query := strings.Join(a.Kinds, ",")
			if a.Path != "" {
				rel, doc, err := docPath(a.Path)
				if err != nil {
					return nil, err
				}
				args["document"] = doc
				query += " " + rel
			}
			if a.Limit > 0 {
				args["limit"] = a.Limit
			}
			res, err := w.call(ctx, c.Agent, "links", args)
			if err != nil {
				return nil, err
			}
			out := drop(res, "text")
			if v, ok := out["deadLinks"]; ok {
				out["deadLinks"] = filterList(v, "", func(m map[string]any) bool {
					if noise(str(m, "target")) {
						return false
					}
					m["sources"] = filterList(m["sources"], "", func(s map[string]any) bool { return !noise(str(s, "source")) })
					srcs, _ := m["sources"].([]any)
					return len(srcs) > 0
				})
			}
			if v, ok := out["orphans"]; ok {
				out["orphans"] = filterList(v, "", func(m map[string]any) bool { return !noise(str(m, "docName")) })
			}
			if v, ok := out["hubs"]; ok {
				// OK counts dead targets as hubs; keep only hubs that exist.
				out["hubs"] = filterList(v, "", func(m map[string]any) bool {
					doc := str(m, "docName")
					return !noise(doc) && w.git.exists(doc+".md")
				})
			}
			c.NoteRead(query, w.pagePaths(out), nil)
			return out, nil
		}),
		registry.New(registry.Operation{
			Name: "wiki_history", Risk: registry.RiskRead, Backend: backend,
			Description: "Edit history of a page, source or trail from OpenKnowledge, newest first, attributed per agent.",
		}, func(ctx context.Context, c *registry.Call, a wikiHistoryArgs) (any, error) {
			if err := w.ready(); err != nil {
				return nil, err
			}
			rel, doc, err := docPath(a.Path)
			if err != nil {
				return nil, err
			}
			args := map[string]any{"document": doc}
			if a.Limit > 0 {
				args["limit"] = a.Limit
			}
			if a.Offset > 0 {
				args["offset"] = a.Offset
			}
			if a.Author != "" {
				args["author"] = a.Author
			}
			res, err := w.call(ctx, c.Agent, "history", args)
			if err != nil {
				return nil, err
			}
			c.NoteRead(rel, nil, []string{rel})
			return drop(res, "text"), nil
		}),
		registry.New(registry.Operation{
			Name: "wiki_audit", Risk: registry.RiskRead, Backend: backend,
			Description: "OpenKnowledge's validation of markdown, frontmatter schemas, OKF rules and internal links. For the full content contract use wiki_lint.",
		}, func(ctx context.Context, c *registry.Call, a wikiAuditArgs) (any, error) {
			if err := w.ready(); err != nil {
				return nil, err
			}
			args := map[string]any{}
			if p := strings.Trim(strings.TrimSpace(a.Path), "/"); p != "" {
				if err := checkContentPath(strings.TrimSuffix(p, ".md")); err != nil {
					return nil, err
				}
				args["path"] = p
			}
			res, err := w.call(ctx, c.Agent, "audit", args)
			if err != nil {
				return nil, err
			}
			return drop(res, "text"), nil
		}),
		registry.New(registry.Operation{
			Name: "wiki_write", Risk: registry.RiskWrite, Tier: registry.TierDefault, Backend: backend,
			Description: "Create or replace a page, source or trail with the whole file (frontmatter included). den-wiki commits it with you as author and (trail:<slug>) in the subject. Search first: one concept, one page.",
			Examples: []registry.Example{{Description: "Write a page during a research run", Arguments: map[string]any{
				"path": "wiki/agent-memory.md", "trail": "agent-memory-research-2026-10-28", "summary": "Add agent memory page",
				"content": "---\ntype: concept\ntitle: Agent memory\ntags: [ai]\ncuration: unreviewed\nupdated: 2026-10-29\n---\n\n# Agent memory\n",
			}}},
		}, func(ctx context.Context, c *registry.Call, a wikiWriteArgs) (any, error) {
			if err := w.ready(); err != nil {
				return nil, err
			}
			rel, doc, err := docPath(a.Path)
			if err != nil {
				return nil, err
			}
			if err := checkTrail(a.Trail); err != nil {
				return nil, err
			}
			w.git.writes.Lock()
			defer w.git.writes.Unlock()
			subj := subject(a.Summary, "Write "+rel, a.Trail)
			// Frontmatter travels inside content: OK stores it byte-exact,
			// while its frontmatter argument drops empty arrays (#10462).
			// OK refuses to overwrite an existing document unless told how;
			// replace is also its default for a new one.
			res, err := w.call(ctx, c.Agent, "write", map[string]any{"document": map[string]any{"path": doc, "content": a.Content, "position": "replace"}, "summary": subj})
			if err != nil {
				return nil, err
			}
			cerr := w.git.waitFor(ctx, func() bool {
				b, ok := w.git.read(rel)
				return ok && string(b) == a.Content
			})
			var cm *Commit
			if cerr == nil {
				cm, cerr = w.git.commit(ctx, c.Agent, subj, []string{rel})
			}
			return writeResult(rel, res, cm, cerr), nil
		}),
		registry.New(registry.Operation{
			Name: "wiki_edit", Risk: registry.RiskWrite, Tier: registry.TierDefault, Backend: backend,
			Description: "Edit a page, source or trail in place: replace exact body text (find, replace) or merge-patch its frontmatter, not both. den-wiki commits the change with you as author and (trail:<slug>).",
		}, func(ctx context.Context, c *registry.Call, a wikiEditArgs) (any, error) {
			if err := w.ready(); err != nil {
				return nil, err
			}
			rel, doc, err := docPath(a.Path)
			if err != nil {
				return nil, err
			}
			if err := checkTrail(a.Trail); err != nil {
				return nil, err
			}
			w.git.writes.Lock()
			defer w.git.writes.Unlock()
			body := a.Find != nil || a.Replace != nil
			switch {
			case body && a.Frontmatter != nil:
				return nil, badArg("give find and replace, or frontmatter, not both")
			case body && (a.Find == nil || a.Replace == nil):
				return nil, badArg("a body edit needs both find and replace")
			case !body && a.Frontmatter == nil:
				return nil, badArg("give find and replace, or frontmatter")
			}
			before, found := w.git.read(rel)
			if !found {
				return nil, &registry.Error{Code: registry.CodeNotFound, Message: rel + " does not exist; create it with wiki_write"}
			}
			d := map[string]any{"path": doc}
			if body {
				d["find"], d["replace"] = *a.Find, *a.Replace
				if a.Occurrence > 0 {
					d["occurrence"] = a.Occurrence
				}
			} else {
				d["frontmatter"] = a.Frontmatter
			}
			subj := subject(a.Summary, "Edit "+rel, a.Trail)
			res, err := w.call(ctx, c.Agent, "edit", map[string]any{"document": d, "summary": subj})
			if err != nil {
				return nil, err
			}
			cerr := w.git.waitFor(ctx, func() bool {
				b, ok := w.git.read(rel)
				return ok && !bytes.Equal(b, before)
			})
			var cm *Commit
			if cerr == nil {
				cm, cerr = w.git.commit(ctx, c.Agent, subj, []string{rel})
			}
			return writeResult(rel, res, cm, cerr), nil
		}),
		registry.New(registry.Operation{
			Name: "wiki_move", Risk: registry.RiskWrite, Backend: backend,
			Description: "Rename or move a document; inline links to it are rewritten. den-wiki commits the move and every rewritten document together.",
		}, func(ctx context.Context, c *registry.Call, a wikiMoveArgs) (any, error) {
			if err := w.ready(); err != nil {
				return nil, err
			}
			from, fromDoc, err := docPath(a.From)
			if err != nil {
				return nil, err
			}
			to, toDoc, err := docPath(a.To)
			if err != nil {
				return nil, err
			}
			if err := checkTrail(a.Trail); err != nil {
				return nil, err
			}
			w.git.writes.Lock()
			defer w.git.writes.Unlock()
			if !w.git.exists(from) {
				return nil, &registry.Error{Code: registry.CodeNotFound, Message: from + " does not exist"}
			}
			if w.git.exists(to) {
				return nil, &registry.Error{Code: "stale_write", Message: to + " already exists; merge the pages instead"}
			}
			subj := subject(a.Summary, "Move "+from+" to "+to, a.Trail)
			res, err := w.call(ctx, c.Agent, "move", map[string]any{"from": fromDoc, "to": toDoc, "summary": subj})
			if err != nil {
				return nil, err
			}
			paths := []string{from, to}
			rewritten := []string{}
			for _, r := range filterList(res["rewrittenDocs"], "", func(map[string]any) bool { return true }) {
				if d := str(r.(map[string]any), "docName"); d != "" {
					paths = append(paths, d+".md")
					rewritten = append(rewritten, d+".md")
				}
			}
			cerr := w.git.waitFor(ctx, func() bool { return !w.git.exists(from) && w.git.exists(to) })
			var cm *Commit
			if cerr == nil {
				cm, cerr = w.git.commit(ctx, c.Agent, subj, paths)
			}
			out := map[string]any{"from": from, "to": to, "rewritten": rewritten, "commit": cm}
			if cerr != nil {
				out["commit_error"] = cerr.Error()
			}
			return out, nil
		}),
		registry.New(registry.Operation{
			Name: "wiki_delete", Risk: registry.RiskDestructive, Backend: backend,
			Description: "Delete a document. Inbound links become dead links, so merge into the page that replaces it first. den-wiki commits the deletion with you as author.",
		}, func(ctx context.Context, c *registry.Call, a wikiDeleteArgs) (any, error) {
			if err := w.ready(); err != nil {
				return nil, err
			}
			rel, doc, err := docPath(a.Path)
			if err != nil {
				return nil, err
			}
			if err := checkTrail(a.Trail); err != nil {
				return nil, err
			}
			w.git.writes.Lock()
			defer w.git.writes.Unlock()
			if !w.git.exists(rel) {
				return nil, &registry.Error{Code: registry.CodeNotFound, Message: rel + " does not exist"}
			}
			if _, err := w.call(ctx, c.Agent, "delete", map[string]any{"document": doc}); err != nil {
				return nil, err
			}
			subj := subject(a.Summary, "Delete "+rel, a.Trail)
			cerr := w.git.waitFor(ctx, func() bool { return !w.git.exists(rel) })
			var cm *Commit
			if cerr == nil {
				cm, cerr = w.git.commit(ctx, c.Agent, subj, []string{rel})
			}
			out := map[string]any{"path": rel, "deleted": true, "commit": cm}
			if cerr != nil {
				out["commit_error"] = cerr.Error()
			}
			return out, nil
		}),
	}
}

// snake converts OK's camelCase keys to den-wiki's snake_case.
func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}
