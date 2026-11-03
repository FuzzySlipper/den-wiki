//go:build okintegration

package facade

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FuzzySlipper/den-wiki/internal/ledger"
	"github.com/FuzzySlipper/den-wiki/internal/lint"
	"github.com/FuzzySlipper/den-wiki/internal/media"
	"github.com/FuzzySlipper/den-wiki/internal/okclient"
	"github.com/FuzzySlipper/den-wiki/internal/testok"
)

// testImage is a w by h PNG of deterministic noise, which PNG cannot
// compress, so a full-size image really is several megabytes.
func testImage(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	seed := uint32(w*7919 + h)
	for i := range img.Pix {
		seed = seed*1664525 + 1013904223
		img.Pix[i] = uint8(seed >> 24)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// testGIF is a three-frame animated GIF.
func testGIF(t *testing.T) []byte {
	t.Helper()
	pal := color.Palette{color.Black, color.White, color.RGBA{255, 0, 0, 255}}
	anim := &gif.GIF{}
	for i := 0; i < 3; i++ {
		f := image.NewPaletted(image.Rect(0, 0, 32, 32), pal)
		for x := 0; x < 32; x++ {
			f.SetColorIndex(x, (x+i*8)%32, uint8(1+i%2))
		}
		anim.Image, anim.Delay = append(anim.Image, f), append(anim.Delay, 20)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, anim); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

const mediaTrail = `---
type: trail
question: Can an agent attach, capture and embed media through den-wiki alone?
started: 2026-11-02T08:00:00Z
agent: alpha
harness: claude-code
---

## Question and scope

Media probe.

## Prior coverage checked

None.

## Queries and methods

None.

## Sources

None.

## Dead ends

None.

## Changes made

None.

## Proposed changes

None.

## Open questions and gaps

None.
`

// The task's done criteria, through the facade against the pinned OK: attach
// a full-size image and an animated GIF to a page, embed a clip kept out of
// git, capture an image as a source and cite it, and keep lint and the
// curation scan clean.
func TestMediaThroughFacade(t *testing.T) {
	stack := testok.Start(t, "../..")
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "den-wiki.db")
	store, err := ledger.OpenSQLite(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	reads, err := OpenReadLog(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reads.Close() })
	mediaDir := t.TempDir()
	d := Deps{
		Ledger: ledger.NewService(store, time.Now, ledger.WithContentDir(stack.ContentDir)),
		Wiki:   NewOKWiki(okclient.New(stack.MCP, 60*time.Second)), ContentDir: stack.ContentDir, Commits: NewCommitter(stack.ContentDir), Reads: reads,
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), Clock: time.Now, Media: &media.Store{Dir: mediaDir},
	}
	reg, err := NewRegistry(d)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewServer(d, reg))
	t.Cleanup(srv.Close)
	h := &harness{srv: srv, reads: reads, logs: &bytes.Buffer{}}
	must := func(tool string, args map[string]any) map[string]any {
		t.Helper()
		sc, isErr := h.call(t, "alpha", tool, args)
		if isErr {
			t.Fatalf("%s: %v", tool, sc)
		}
		return sc
	}
	b64 := base64.StdEncoding.EncodeToString
	const trail = "media-probe-2026-11-02"
	must("wiki_write", map[string]any{"path": "trails/" + trail + ".md", "content": mediaTrail, "trail": trail})

	// A full-size image (several MB of PNG, past the old 4 MiB MCP limit) and
	// an animated GIF go into git beside the page.
	card := testImage(t, 2400, 1350)
	if len(card) < 4<<20 {
		t.Fatalf("test card is only %d bytes; it should exceed the old MCP limit", len(card))
	}
	for name, data := range map[string][]byte{"test-card.png": card, "test-pattern.gif": testGIF(t)} {
		r := must("wiki_attach", map[string]any{"page": "wiki/formatting-reference.md", "name": name, "content_base64": b64(data), "trail": trail})
		want := "wiki/formatting-reference.assets/" + name
		if r["stored"] != "git" || r["path"] != want || r["commit"] == nil {
			t.Fatalf("attach %s: %v", name, r)
		}
		got, err := os.ReadFile(filepath.Join(stack.ContentDir, want))
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("%s on disk differs from what was attached", name)
		}
		if show := gitIn(t, stack.ContentDir, "log", "-1", "--format=%an %s", "--", want); show != "alpha Attach "+want+" (trail:"+trail+")" {
			t.Fatalf("commit for %s: %q", name, show)
		}
	}

	// A clip goes to the media store; git does not grow by its size.
	clip := bytes.Repeat([]byte("not really H.264, but the store does not care "), 4000)
	before := gitIn(t, stack.ContentDir, "rev-parse", "HEAD")
	v := must("wiki_attach", map[string]any{"page": "wiki/formatting-reference.md", "name": "test-clip.mp4", "content_base64": b64(clip), "trail": trail})
	clipName := media.Hash(clip) + ".mp4"
	if v["stored"] != "media" || v["url"] != "/media/"+clipName {
		t.Fatalf("attach clip: %v", v)
	}
	if after := gitIn(t, stack.ContentDir, "rev-parse", "HEAD"); after != before {
		t.Fatalf("attaching a clip made a commit")
	}

	// The formatting reference page, with every supported construct, lands
	// byte for byte with no broken links once the media store holds the clip.
	tmpl, err := os.ReadFile("testdata/formatting-reference.md")
	if err != nil {
		t.Fatal(err)
	}
	ref := strings.ReplaceAll(string(tmpl), "{{CLIP}}", strings.TrimSuffix(clipName, ".mp4"))
	w := must("wiki_write", map[string]any{"path": "wiki/formatting-reference.md", "content": ref, "trail": trail})
	if bl, _ := w["broken_links"].([]any); len(bl) != 0 {
		t.Fatalf("reference page broken links: %v", bl)
	}
	for _, warn := range listOf(w["warnings"]) {
		if strings.Contains(str(warn, "code"), "dead-link") || strings.Contains(str(warn, "kind"), "mermaid") {
			t.Fatalf("reference page warning: %v", warn)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(stack.ContentDir, "wiki/formatting-reference.md")); string(got) != ref {
		t.Fatalf("reference page on disk differs from what was written")
	}

	// A captured image as a source, cited from a page that also shows it.
	frame := testImage(t, 640, 360)
	fr := must("wiki_attach", map[string]any{"page": "sources/media-probe/strike-frame.md", "name": "frame.png", "content_base64": b64(frame), "trail": trail})
	body := "\nFrame 120 of the strike footage, captured at its original 640 by 360 pixels.\n"
	src := "---\ntype: source\ntitle: Strike footage, frame 120\nsource_url: https://example.org/footage/strike\ncaptured: 2026-11-02\nsha256: \"" + lintHash(body) + "\"\ncaptured_by_trail: " + trail +
		"\nmedia: " + fr["path"].(string) + "\nmedia_sha256: \"" + media.Hash(frame) + "\"\n---\n" + body
	must("wiki_write", map[string]any{"path": "sources/media-probe/strike-frame.md", "content": src, "trail": trail})
	page := "---\ntype: concept\ntitle: Strike imagery\ntopic: agents\ntags: [ai]\ntrails: [" + trail + "]\ncuration: unreviewed\nupdated: 2026-11-02\n---\n\n# Strike imagery\n\n" +
		"![Frame 120 of the strike footage](../sources/media-probe/strike-frame.assets/frame.png)\n\n*Figure 1. Frame 120.*\n\n" +
		"The frame shows the strike from the north.[^1]\n\n[^1]: [The captured frame](../sources/media-probe/strike-frame.md) \"Frame 120 of the strike footage\"\n"
	pw := must("wiki_write", map[string]any{"path": "wiki/strike-imagery.md", "content": page, "trail": trail})
	if bl, _ := pw["broken_links"].([]any); len(bl) != 0 {
		t.Fatalf("strike page broken links: %v", bl)
	}

	// Lint, with the media store, finds nothing, commits included.
	fs, err := lint.Lint(stack.ContentDir, lint.Options{MediaDir: mediaDir, Commits: stack.Seed + "..HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 0 {
		var b strings.Builder
		for _, f := range fs {
			b.WriteString("\n  " + f.String())
		}
		t.Fatalf("lint findings:%s", b.String())
	}
	// The curation scan does not call the store's files dead links.
	scan := must("wiki_curation_scan", map[string]any{})
	for _, it := range listOf(scan["items"]) {
		if str(it, "class") == "dead_link" || str(it, "class") == "source" && strings.Contains(str(it, "path"), "strike-frame") {
			t.Errorf("curation item: %v", it)
		}
	}

	// The facade serves the clip.
	resp, err := http.Get(srv.URL + "/media/" + clipName)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(got, clip) {
		t.Fatalf("serve clip: %d, %d bytes", resp.StatusCode, len(got))
	}
}

func lintHash(body string) string { return media.Hash([]byte(body)) }
