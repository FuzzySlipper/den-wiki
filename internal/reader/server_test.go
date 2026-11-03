package reader

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeBackend struct {
	err   error
	hits  []Hit
	brief Briefing
}

func (f *fakeBackend) Briefing(context.Context, string) (Briefing, error) { return f.brief, f.err }

func (f *fakeBackend) Search(context.Context, string, int) ([]Hit, error) { return f.hits, f.err }

const taxonomy = `version: 1
types: [{name: concept, description: c}, {name: hub, description: h}]
tags: [{name: software, description: s}]
topics:
  - {name: dev, description: Software development in general.}
  - {name: gamedev, description: Game design and engines., parent: dev}
  - {name: home, description: The house.}
`

func page(title, topic, typ, body string) string {
	fm := "---\ntype: " + typ + "\ntitle: " + title + "\ntags: [software]\ncuration: unreviewed\nupdated: 2026-11-01\n"
	if topic != "" {
		fm += "topic: " + topic + "\n"
	}
	return fm + "---\n\n" + body + "\n"
}

func newTestServer(t *testing.T, b Backend) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"taxonomy.yml":                   taxonomy,
		"wiki/index.md":                  "generated",
		"wiki/spellcasting.md":           page("Skill-based spellcasting", "gamedev", "concept", "Spells drawn by hand.[^1]\n\n## Examples\n\nSee [engines](engines.md) and [nothing](nowhere.md).\n\n### Detail\n\nMore.\n\n[^1]: [The survey page](../sources/survey.md) \"spells are drawn\""),
		"wiki/engines.md":                page("Game engines", "gamedev", "hub", "A map of engines."),
		"wiki/boiler.md":                 page("Boiler", "home", "concept", "The boiler."),
		"wiki/loose.md":                  page("Loose page", "", "concept", "No topic."),
		"wiki/odd.md":                    page("Odd topic", "cooking", "concept", "Unknown topic."),
		"wiki/spellcasting.assets/a.png": "\x89PNG",
		"wiki/spellcasting.assets/x.svg": "<svg/>",
		"sources/survey.md":              "---\ntype: source\ntitle: A survey\nsource_url: https://example.org/s\ncaptured: 2026-10-01\ncaptured_by_trail: run\n---\n\nCaptured text.\n",
		"trails/run.md":                  "---\ntype: trail\nquestion: How are spells drawn?\nstarted: 2026-10-01\n---\n\n## Question and scope\n\nq\n",
		"wiki/.ok/frontmatter.yml":       "zone: wiki",
	}
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(Config{ContentDir: dir, EditURL: "https://edit.example/", Backend: b})
	if err != nil {
		t.Fatal(err)
	}
	s.site.ttl = 0
	t.Cleanup(func() { s.Close() })
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return srv, dir
}

func get(t *testing.T, srv *httptest.Server, path string) (int, http.Header, string) {
	t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

func mustContain(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q", w)
		}
	}
}

func TestHome(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	code, h, body := get(t, srv, "/")
	if code != 200 || !strings.HasPrefix(h.Get("Content-Type"), "text/html") || !strings.Contains(h.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("%d %v", code, h)
	}
	mustContain(t, body,
		"5 pages, 5 not yet reviewed.",
		`<a href="/topic/dev">Dev</a> <span class="count">2</span>`,
		`<a href="/topic/gamedev">Gamedev</a> <span class="count">2</span>`,
		`<a class="tree-topic" href="/unsorted">Unsorted</a> <span class="count">2</span>`,
		"Recently updated",
	)
}

func TestTopicPage(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	code, _, body := get(t, srv, "/topic/gamedev")
	if code != 200 {
		t.Fatal(code)
	}
	mustContain(t, body, `<a href="/">Home</a> › <a href="/topic/dev">Dev</a>`, "<h1>Gamedev</h1>", "Game design and engines.", `class="tree-topic current" href="/topic/gamedev"`, "<details open>")
	// Hubs first, then by title.
	if strings.Index(body, `<a href="/wiki/engines">Game engines</a> <span class="badge">hub</span>`) > strings.Index(body, `<li><a href="/wiki/spellcasting">Skill-based spellcasting</a>`) {
		t.Error("hub not listed first")
	}
	if code, _, _ := get(t, srv, "/topic/nope"); code != 404 {
		t.Errorf("unknown topic: %d", code)
	}
	_, _, body = get(t, srv, "/unsorted")
	mustContain(t, body, "Loose page", "Odd topic")
}

func TestPage(t *testing.T) {
	b := &fakeBackend{brief: Briefing{
		Backlinks: []Backlink{{Path: "wiki/engines.md", Title: "Game engines"}},
		History:   []Change{{Date: "2026-11-01", Author: "claude-code", Subject: "Add examples (trail:run)", Trail: "run"}},
	}}
	srv, _ := newTestServer(t, b)
	code, _, body := get(t, srv, "/wiki/spellcasting")
	if code != 200 {
		t.Fatal(code)
	}
	mustContain(t, body,
		"<title>Skill-based spellcasting · den-wiki</title>",
		`<a href="/topic/dev">Dev</a> › <a href="/topic/gamedev">Gamedev</a>`,
		`<a class="edit" href="https://edit.example/#/wiki/spellcasting">Edit in OpenKnowledge</a>`,
		`<li class="toc-2"><a href="#examples">Examples</a></li>`,
		`<li class="toc-3"><a href="#detail">Detail</a></li>`,
		`<li class="toc-2"><a href="#references">References</a></li>`,
		`<dt>Topic</dt><dd><a href="/topic/gamedev">Gamedev</a></dd>`,
		`<dt>Citations</dt><dd><a href="#references">1</a></dd>`,
		`<a href="/wiki/nowhere" class="new"`,
		`<a class="original" href="https://example.org/s"`,
		`<li><a href="/wiki/engines">Game engines</a></li>`,
		`Add examples (trail:run) <a href="/trails/run">trail</a>`,
		`<a href="/wiki/spellcasting" class="current" aria-current="page">`,
		`/static/reader.js`,
	)
	if strings.Contains(body, "katex") || strings.Contains(body, "mermaid.min.js") {
		t.Error("scripts loaded for a page without math or diagrams")
	}
}

func TestSourceAndTrail(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	_, _, body := get(t, srv, "/sources/survey")
	mustContain(t, body, "<h1>A survey</h1>", `<dt>Original</dt><dd><a href="https://example.org/s"`, `<dt>By trail</dt><dd><a href="/trails/run">run</a>`, "Captured source")
	_, _, body = get(t, srv, "/trails/run")
	mustContain(t, body, "<h1>How are spells drawn?</h1>", "Research trail")
}

func TestBackendDown(t *testing.T) {
	srv, _ := newTestServer(t, &fakeBackend{err: errors.New("connection refused")})
	code, _, body := get(t, srv, "/wiki/spellcasting")
	if code != 200 {
		t.Fatal(code)
	}
	mustContain(t, body, "Backlinks and history are unavailable", "Spells drawn by hand.")
	_, _, body = get(t, srv, "/search?q=spells")
	mustContain(t, body, "Search is unavailable")
	srv, _ = newTestServer(t, nil)
	_, _, body = get(t, srv, "/search?q=spells")
	mustContain(t, body, "Search needs OpenKnowledge")
}

func TestSearch(t *testing.T) {
	srv, _ := newTestServer(t, &fakeBackend{hits: []Hit{
		{Path: "trails/run.md", Title: "trails/run", Snippet: "--- type: trail"},
		{Path: "wiki/spellcasting.md", Title: "Skill-based spellcasting", Snippet: "drawn by hand"},
	}})
	_, _, body := get(t, srv, "/search?q=spells")
	mustContain(t, body, `value="spells"`, "<p>drawn by hand</p>")
	if strings.Index(body, "/wiki/spellcasting") > strings.Index(body, `href="/trails/run"`) || strings.Contains(body, "--- type") {
		t.Errorf("wiki pages not first or frontmatter snippet shown")
	}
}

func TestFilesAndPaths(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	code, h, body := get(t, srv, "/wiki/spellcasting.assets/a.png")
	if code != 200 || h.Get("Content-Type") != "image/png" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") || body != "\x89PNG" {
		t.Errorf("asset: %d %v", code, h)
	}
	if _, h, _ := get(t, srv, "/wiki/spellcasting.assets/x.svg"); !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Error("svg not sandboxed")
	}
	if code, h, _ := get(t, srv, "/wiki/spellcasting.md"); code != 301 || h.Get("Location") != "/wiki/spellcasting" {
		t.Errorf("md redirect: %d %v", code, h)
	}
	code, _, body = get(t, srv, "/wiki/missing-page")
	if code != 404 || !strings.Contains(body, `<a href="https://edit.example/#/wiki/missing-page">create it in OpenKnowledge</a>`) {
		t.Errorf("missing page: %d", code)
	}
	for _, p := range []string{"/wiki/.ok/frontmatter", "/wiki/../taxonomy", "/wiki/%2e%2e/taxonomy", "/taxonomy.yml", "/wiki/spellcasting.assets/nope.png"} {
		// The mux redirects dot-dot paths to their clean form, which 404s.
		if code, _, _ := get(t, srv, p); code != 404 && code != 307 {
			t.Errorf("%s: %d", p, code)
		}
	}
	for _, p := range []string{"/static/reader.css", "/static/reader.js", "/static/vendor/mermaid.min.js", "/static/vendor/katex/katex.min.css", "/static/vendor/katex/fonts/KaTeX_Main-Regular.woff2"} {
		if code, _, _ := get(t, srv, p); code != 200 {
			t.Errorf("%s: %d", p, code)
		}
	}
}

func TestLibraryFollowsEdits(t *testing.T) {
	srv, dir := newTestServer(t, nil)
	_, _, body := get(t, srv, "/topic/home")
	mustContain(t, body, "Boiler")
	if err := os.WriteFile(filepath.Join(dir, "wiki/boiler.md"), []byte(page("Boiler and radiators", "dev", "concept", "Moved.")), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, body = get(t, srv, "/topic/dev")
	mustContain(t, body, "Boiler and radiators")
	_, _, body = get(t, srv, "/wiki/boiler")
	mustContain(t, body, "<h1>Boiler and radiators</h1>", "<p>Moved.</p>")
}

func TestScriptsLoadOnlyWhenNeeded(t *testing.T) {
	srv, dir := newTestServer(t, nil)
	if err := os.WriteFile(filepath.Join(dir, "wiki/maths.md"), []byte(page("Maths", "dev", "concept", "$x^2$\n\n```mermaid\ngraph LR\n  A --> B\n```")), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, body := get(t, srv, "/wiki/maths")
	mustContain(t, body, "/static/vendor/katex/katex.min.css", "/static/vendor/katex/katex.min.js", "/static/vendor/mermaid.min.js")
}

// A cached render follows the files it links to and cites, not only its own
// file (review R10558-1).
func TestCachedRenderFollowsLinkedFiles(t *testing.T) {
	srv, dir := newTestServer(t, nil)
	write := func(rel, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("wiki/linker.md", page("Linker", "dev", "concept", "See [later](later.md).[^1]\n\n[^1]: [The survey page](../sources/survey.md) \"spells are drawn\""))
	red := `<a href="/wiki/later" class="new"`
	_, _, body := get(t, srv, "/wiki/linker")
	mustContain(t, body, red, `<a class="original" href="https://example.org/s"`)

	write("wiki/later.md", page("Later", "dev", "concept", "Now it exists."))
	_, _, body = get(t, srv, "/wiki/linker")
	if strings.Contains(body, red) || !strings.Contains(body, `<a href="/wiki/later">later</a>`) {
		t.Error("link still red after its target was created")
	}

	if err := os.Remove(filepath.Join(dir, "wiki/later.md")); err != nil {
		t.Fatal(err)
	}
	_, _, body = get(t, srv, "/wiki/linker")
	mustContain(t, body, red)

	write("sources/survey.md", "---\ntype: source\ntitle: A survey\nsource_url: https://example.org/moved-survey\ncaptured: 2026-10-01\ncaptured_by_trail: run\n---\n\nCaptured text.\n")
	_, _, body = get(t, srv, "/wiki/linker")
	mustContain(t, body, `<a class="original" href="https://example.org/moved-survey"`)
}
