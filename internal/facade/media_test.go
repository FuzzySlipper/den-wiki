package facade

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FuzzySlipper/den-wiki/internal/media"
	"github.com/FuzzySlipper/den-wiki/internal/okclient"
)

// assetWiki stands in for OpenKnowledge's write: it applies document and
// asset writes to the content directory, and reports the brokenLinks given
// for a document write.
type assetWiki struct {
	dir    string
	broken []map[string]any
}

func (w *assetWiki) Ping(context.Context) error { return nil }

func (w *assetWiki) Call(_ context.Context, _, tool string, args any) (okclient.CallResult, error) {
	m := args.(map[string]any)
	if tool != "write" {
		return okclient.CallResult{Text: "{}", Structured: []byte("{}")}, nil
	}
	if a, ok := m["asset"].(map[string]any); ok {
		b, _ := base64.StdEncoding.DecodeString(a["content"].(string))
		rel := a["path"].(string)
		// Like OK, keep one copy of identical bytes in a folder.
		if same := w.identical(filepath.Dir(rel), b); same != "" {
			rel = same
		} else {
			p := filepath.Join(w.dir, filepath.FromSlash(rel))
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			_ = os.WriteFile(p, b, 0o644)
		}
		out, _ := json.Marshal(map[string]any{"asset": map[string]any{"ok": true, "path": rel}})
		return okclient.CallResult{Text: "{}", Structured: out}, nil
	}
	d := m["document"].(map[string]any)
	p := filepath.Join(w.dir, d["path"].(string)+".md")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte(d["content"].(string)), 0o644)
	out, _ := json.Marshal(map[string]any{"document": map[string]any{"brokenLinks": w.broken}})
	return okclient.CallResult{Text: "{}", Structured: out}, nil
}

func (w *assetWiki) identical(dir string, b []byte) string {
	entries, _ := os.ReadDir(filepath.Join(w.dir, dir))
	for _, e := range entries {
		if have, err := os.ReadFile(filepath.Join(w.dir, dir, e.Name())); err == nil && string(have) == string(b) {
			return dir + "/" + e.Name()
		}
	}
	return ""
}

func mediaHarness(t *testing.T, gitMax int64) (*harness, string, string, *assetWiki) {
	t.Helper()
	dir, store := t.TempDir(), t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	put(t, dir, "wiki/strikes.md", "page\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "seed")
	wiki := &assetWiki{dir: dir}
	h := newHarnessDeps(t, wiki, dir, func(d *Deps) {
		d.Media, d.MediaGitMax = &media.Store{Dir: store}, gitMax
	})
	return h, dir, store, wiki
}

func TestAttachImageGoesIntoGit(t *testing.T) {
	h, dir, _, _ := mediaHarness(t, 0)
	img := []byte("\x89PNG full-size image bytes")
	out, isErr := h.call(t, "alice", "wiki_attach", map[string]any{
		"page": "wiki/strikes.md", "name": "strike-map.png", "content_base64": base64.StdEncoding.EncodeToString(img),
		"alt": "Strike map", "trail": "strikes-run",
	})
	if isErr {
		t.Fatalf("attach: %v", out)
	}
	if out["stored"] != "git" || out["path"] != "wiki/strikes.assets/strike-map.png" || out["sha256"] != media.Hash(img) {
		t.Fatalf("attach result: %v", out)
	}
	if out["embed"] != "![Strike map](./strikes.assets/strike-map.png)" {
		t.Errorf("embed: %v", out["embed"])
	}
	log := gitIn(t, dir, "log", "-1", "--format=%an|%s", "--name-only")
	if !strings.Contains(log, "alice|Attach wiki/strikes.assets/strike-map.png (trail:strikes-run)") || !strings.Contains(log, "wiki/strikes.assets/strike-map.png") {
		t.Fatalf("commit:\n%s", log)
	}
	if got := gitIn(t, dir, "show", "HEAD:wiki/strikes.assets/strike-map.png"); got != string(img) {
		t.Fatalf("committed bytes differ")
	}

	// The same bytes under another name: OK keeps its one copy, and the
	// embed points at it.
	again, isErr := h.call(t, "alice", "wiki_attach", map[string]any{
		"page": "wiki/strikes.md", "name": "map-copy.png", "content_base64": base64.StdEncoding.EncodeToString(img), "trail": "strikes-run",
	})
	if isErr || again["path"] != "wiki/strikes.assets/strike-map.png" || again["deduplicated"] != true || again["embed"] != "![map-copy.png](./strikes.assets/strike-map.png)" {
		t.Fatalf("duplicate attach: %v", again)
	}
}

func TestRelativeTo(t *testing.T) {
	for _, c := range []struct{ dir, target, want string }{
		{"wiki", "wiki/strikes.assets/a.png", "./strikes.assets/a.png"},
		{"wiki/ai", "wiki/ai/rag.assets/a.png", "./rag.assets/a.png"},
		{"wiki/ai", "wiki/strikes.assets/a.png", "../strikes.assets/a.png"},
		{"wiki", "sources/x/clip.assets/f.png", "../sources/x/clip.assets/f.png"},
	} {
		if got := relativeTo(c.dir, c.target); got != c.want {
			t.Errorf("relativeTo(%q, %q) = %q, want %q", c.dir, c.target, got, c.want)
		}
	}
}

func TestAttachVideoGoesToTheMediaStore(t *testing.T) {
	h, dir, store, _ := mediaHarness(t, 0)
	clip := []byte(strings.Repeat("mp4 frame ", 1000))
	head := gitIn(t, dir, "rev-parse", "HEAD")
	out, isErr := h.call(t, "alice", "wiki_attach", map[string]any{
		"page": "wiki/strikes.md", "name": "strike.mp4", "content_base64": base64.StdEncoding.EncodeToString(clip), "trail": "strikes-run",
	})
	if isErr {
		t.Fatalf("attach: %v", out)
	}
	name := media.Hash(clip) + ".mp4"
	if out["stored"] != "media" || out["url"] != "/media/"+name {
		t.Fatalf("attach result: %v", out)
	}
	if !strings.HasPrefix(out["embed"].(string), `<video src="/media/`+name+`" controls`) {
		t.Errorf("embed: %v", out["embed"])
	}
	if got := gitIn(t, dir, "rev-parse", "HEAD"); got != head {
		t.Errorf("a media store attach made a commit")
	}
	if b, err := os.ReadFile(filepath.Join(store, name)); err != nil || string(b) != string(clip) {
		t.Fatalf("stored file: %v", err)
	}

	// The facade serves it, with ranges so video can seek.
	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/media/"+name, nil)
	req.Header.Set("Range", "bytes=0-9")
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || string(body) != "mp4 frame " || resp.Header.Get("Content-Type") != "video/mp4" {
		t.Fatalf("serve: %d %q %s", resp.StatusCode, body, resp.Header.Get("Content-Type"))
	}
}

func TestAttachLargeImageGoesToTheMediaStore(t *testing.T) {
	h, _, store, _ := mediaHarness(t, 10)
	img := []byte("a large gif, past the git limit")
	out, isErr := h.call(t, "alice", "wiki_attach", map[string]any{
		"page": "wiki/strikes.md", "name": "loop.gif", "content_base64": base64.StdEncoding.EncodeToString(img), "alt": "Loop", "trail": "strikes-run",
	})
	if isErr || out["stored"] != "media" {
		t.Fatalf("attach: %v", out)
	}
	if out["embed"] != "![Loop](/media/"+media.Hash(img)+".gif)" {
		t.Errorf("embed: %v", out["embed"])
	}
	if _, err := os.Stat(filepath.Join(store, media.Hash(img)+".gif")); err != nil {
		t.Fatal(err)
	}
}

func TestAttachRejects(t *testing.T) {
	h, _, _, _ := mediaHarness(t, 0)
	b64 := base64.StdEncoding.EncodeToString([]byte("x"))
	for _, c := range []map[string]any{
		{"page": "wiki/strikes.md", "name": "Strike Map.png", "content_base64": b64, "trail": "r"},
		{"page": "wiki/strikes.md", "name": "notes.txt", "content_base64": b64, "trail": "r"},
		{"page": "wiki/strikes.md", "name": "clip.mp4", "content_base64": b64, "trail": "r", "store": "git"},
		{"page": "wiki/strikes.md", "name": "a.png", "content_base64": "not base64!", "trail": "r"},
		{"page": "wiki/strikes.md", "name": "a.png", "content_base64": b64, "trail": "Not A Slug"},
		{"page": "wiki/strikes", "name": "a.png", "content_base64": b64, "trail": "r"},
	} {
		if out, isErr := h.call(t, "alice", "wiki_attach", c); !isErr || errorCode(out) != "invalid_arguments" {
			t.Errorf("%v: %v", c, out)
		}
	}
	// No store configured: video cannot be attached.
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	noStore := newHarnessWith(t, &assetWiki{dir: dir}, dir)
	out, isErr := noStore.call(t, "alice", "wiki_attach", map[string]any{"page": "wiki/a.md", "name": "clip.mp4", "content_base64": b64, "trail": "r"})
	if !isErr || errorCode(out) != "backend_unavailable" {
		t.Errorf("no store: %v", out)
	}
}

// OK calls a link into the media store dead; the facade drops those the
// store holds and keeps the rest.
func TestWriteDropsBrokenLinksTheMediaStoreHolds(t *testing.T) {
	h, dir, store, wiki := mediaHarness(t, 0)
	put(t, dir, "wiki/strikes.assets/just-attached.png", "png")
	held, err := (&media.Store{Dir: store}).Put("png", []byte("held"))
	if err != nil {
		t.Fatal(err)
	}
	missing := media.Hash([]byte("missing")) + ".png"
	wiki.broken = []map[string]any{
		{"href": "/media/" + held, "resolvedTo": "media/" + held},
		{"href": "/media/" + missing, "resolvedTo": "media/" + missing},
		{"href": "big.png", "resolvedTo": "wiki/big.png"},
		{"href": "./strikes.assets/just-attached.png", "resolvedTo": "wiki/strikes.assets/just-attached.png"},
	}
	out, isErr := h.call(t, "alice", "wiki_write", map[string]any{"path": "wiki/strikes.md", "content": "page two\n", "trail": "strikes-run"})
	if isErr {
		t.Fatalf("write: %v", out)
	}
	raw, _ := json.Marshal(out["broken_links"])
	if strings.Contains(string(raw), held) || strings.Contains(string(raw), "just-attached") || !strings.Contains(string(raw), missing) || !strings.Contains(string(raw), "wiki/big.png") {
		t.Fatalf("broken_links: %s", raw)
	}
}
