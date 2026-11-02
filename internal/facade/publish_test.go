package facade

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sharedPageDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"wiki/agent-memory.md":              "---\ntitle: Agent memory\n---\n\n# Agent memory\n\nMemory helps.[^1] See [tools](tools.md).\n\n![Loop](./agent-memory.assets/loop.png)\n\n[^1]: [The survey](../sources/survey.md) \"memory helps\"\n",
		"wiki/agent-memory.assets/loop.png": "png",
		"sources/survey.md":                 "---\nsource_url: https://example.org/survey\n---\n",
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
	return dir
}

func get(t *testing.T, url string) (int, http.Header, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, b
}

func TestExportEndpointsServeOnePageAndItsAssets(t *testing.T) {
	dir := sharedPageDir(t)
	h := newHarnessDeps(t, nil, "", func(d *Deps) { d.ContentDir = dir })
	status, _, body := get(t, h.srv.URL+"/v1/export?page=wiki/agent-memory.md&asset_base=https://blog.example/a/")
	if status != http.StatusOK {
		t.Fatalf("export %d: %s", status, body)
	}
	var p struct {
		Title    string `json:"title"`
		Markdown string `json:"markdown"`
		Assets   []struct {
			Name   string `json:"name"`
			SHA256 string `json:"sha256"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	if p.Title != "Agent memory" || !strings.Contains(p.Markdown, "![Loop](https://blog.example/a/loop.png)") ||
		!strings.Contains(p.Markdown, "See tools.") || !strings.Contains(p.Markdown, "(https://example.org/survey)") {
		t.Errorf("export %+v", p)
	}
	if len(p.Assets) != 1 || p.Assets[0].Name != "loop.png" {
		t.Fatalf("assets %+v", p.Assets)
	}
	status, hdr, b := get(t, h.srv.URL+"/v1/export/asset?page=wiki/agent-memory.md&name=loop.png")
	if status != http.StatusOK || string(b) != "png" || hdr.Get("Content-Type") != "image/png" || hdr.Get("X-Content-SHA256") != p.Assets[0].SHA256 {
		t.Errorf("asset %d %q %v", status, b, hdr)
	}
	for url, want := range map[string]int{
		"/v1/export?page=sources/survey.md":                       http.StatusBadRequest,
		"/v1/export?page=wiki/missing.md":                         http.StatusNotFound,
		"/v1/export/asset?page=wiki/agent-memory.md&name=../x.md": http.StatusNotFound,
	} {
		if status, _, b := get(t, h.srv.URL+url); status != want {
			t.Errorf("%s: %d, want %d: %s", url, status, want, b)
		}
	}
}

func TestWikiPublishAsksDocPublish(t *testing.T) {
	type seen struct {
		path string
		body map[string]any
	}
	var got []seen
	docPublish := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got = append(got, seen{r.URL.Path, body})
		if strings.HasSuffix(r.URL.Path, "/preview") {
			writeJSON(w, http.StatusOK, map[string]any{"status": "previewed", "public_url": "https://blog.example/agent-memory/"})
			return
		}
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]string{"code": "repo_safety_check_failed", "message": "repo has uncommitted changes"}})
	}))
	t.Cleanup(docPublish.Close)
	h := newHarnessDeps(t, nil, "", func(d *Deps) {
		d.ContentDir = sharedPageDir(t)
		d.PublishURL = docPublish.URL + "/api/v1/blog/publications"
	})

	sc, isErr := h.call(t, "patch-agent", "wiki_publish", map[string]any{"path": "wiki/agent-memory.md", "tags": []string{"den"}})
	if isErr || sc["status"] != "previewed" {
		t.Fatalf("preview: %v", sc)
	}
	if len(got) != 1 || got[0].path != "/api/v1/blog/publications/preview" {
		t.Fatalf("requests %+v", got)
	}
	b := got[0].body
	if src := b["source"].(map[string]any); src["wiki_page"] != "wiki/agent-memory.md" || b["requested_by"] != "patch-agent" {
		t.Errorf("request %v", b)
	}

	sc, isErr = h.call(t, "patch-agent", "wiki_publish", map[string]any{"path": "wiki/agent-memory.md", "publish": true})
	if !isErr || errorCode(sc) != "conflict" || !strings.Contains(sc["error"].(map[string]any)["message"].(string), "uncommitted changes") {
		t.Errorf("publish: %v", sc)
	}
	if got[1].path != "/api/v1/blog/publications" {
		t.Errorf("publish went to %s", got[1].path)
	}

	// A page that cannot be exported never reaches doc-publish.
	sc, isErr = h.call(t, "patch-agent", "wiki_publish", map[string]any{"path": "wiki/missing.md"})
	if !isErr || errorCode(sc) != "not_found" || len(got) != 2 {
		t.Errorf("missing page: %v, %d requests", sc, len(got))
	}
}

func TestWikiPublishNeedsDocPublishConfigured(t *testing.T) {
	h := newHarnessDeps(t, nil, "", func(d *Deps) { d.ContentDir = sharedPageDir(t) })
	sc, isErr := h.call(t, "patch-agent", "wiki_publish", map[string]any{"path": "wiki/agent-memory.md"})
	if !isErr || errorCode(sc) != "backend_unavailable" {
		t.Errorf("unconfigured: %v", sc)
	}
}
