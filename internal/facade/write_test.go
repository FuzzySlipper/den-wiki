package facade

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FuzzySlipper/den-wiki/internal/okclient"
)

// diskWiki stands in for OpenKnowledge's write and edit: it applies them to
// the content directory. While alpha's edit is on disk but not yet returned,
// it waits up to hold for beta's write to arrive, which is exactly the window
// where an unserialised facade would let beta's content into alpha's commit.
type diskWiki struct {
	dir          string
	hold         time.Duration
	alphaApplied chan struct{}
	betaArrived  chan struct{}
	once         sync.Once
}

func (w *diskWiki) Ping(context.Context) error { return nil }

func (w *diskWiki) Call(_ context.Context, identity, tool string, args any) (okclient.CallResult, error) {
	d := args.(map[string]any)["document"].(map[string]any)
	p := filepath.Join(w.dir, d["path"].(string)+".md")
	switch {
	case tool == "edit" && identity == "alpha":
		b, _ := os.ReadFile(p)
		_ = os.WriteFile(p, []byte(strings.Replace(string(b), d["find"].(string), d["replace"].(string), 1)), 0o644)
		close(w.alphaApplied)
		select {
		case <-w.betaArrived:
		case <-time.After(w.hold):
		}
	case tool == "write":
		w.once.Do(func() { close(w.betaArrived) })
		_ = os.WriteFile(p, []byte(d["content"].(string)), 0o644)
	}
	return okclient.CallResult{Text: "{}", Structured: []byte("{}")}, nil
}

func TestOverlappingWritesKeepTheirOwnAttribution(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	put(t, dir, "wiki/a.md", "start\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "seed")
	seed := gitIn(t, dir, "rev-parse", "HEAD")
	wiki := &diskWiki{dir: dir, hold: time.Second, alphaApplied: make(chan struct{}), betaArrived: make(chan struct{})}
	h := newHarnessWith(t, wiki, dir)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		h.call(t, "alpha", "wiki_edit", map[string]any{"path": "wiki/a.md", "find": "start", "replace": "alpha", "trail": "alpha-run"})
	}()
	<-wiki.alphaApplied
	go func() {
		defer wg.Done()
		h.call(t, "beta", "wiki_write", map[string]any{"path": "wiki/a.md", "content": "beta\n", "trail": "beta-run"})
	}()
	wg.Wait()

	log := gitIn(t, dir, "log", "--reverse", "--format=%H|%an|%s", seed+"..HEAD")
	lines := strings.Split(log, "\n")
	if len(lines) != 2 {
		t.Fatalf("want one commit per write, got:\n%s", log)
	}
	want := []struct{ author, trail, content string }{{"alpha", "(trail:alpha-run)", "alpha\n"}, {"beta", "(trail:beta-run)", "beta\n"}}
	for i, line := range lines {
		f := strings.SplitN(line, "|", 3)
		got := gitIn(t, dir, "show", f[0]+":wiki/a.md")
		if f[1] != want[i].author || !strings.HasSuffix(f[2], want[i].trail) || got+"\n" != want[i].content {
			t.Errorf("commit %d: author %s, subject %q, content %q; want %s, %s, %q", i, f[1], f[2], got, want[i].author, want[i].trail, want[i].content)
		}
	}
}

func TestRegeneratedIndexesAreCommittedSeparately(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	put(t, dir, "index.md", "root index\n")
	put(t, dir, "wiki/a.md", "a\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "seed")
	// OK regenerates a tracked index (an unstaged " M") and adds a new one;
	// someone else has a file staged.
	put(t, dir, "index.md", "root index, regenerated\n")
	put(t, dir, "wiki/ai/index.md", "new folder index\n")
	put(t, dir, "wiki/staged.md", "staged\n")
	gitIn(t, dir, "add", "wiki/staged.md")
	put(t, dir, "wiki/a.md", "a v2\n")

	c := &committer{dir: dir}
	cm, err := c.commit(context.Background(), "alpha", "Edit a (trail:run)", []string{"wiki/a.md"})
	if err != nil || cm == nil || cm.IndexCommitError != "" {
		t.Fatalf("commit: %+v %v", cm, err)
	}
	if got := gitIn(t, dir, "show", "--name-only", "--format=%an|%s", "HEAD"); got != "den-wiki|Regenerate indexes\n\nindex.md\nwiki/ai/index.md" {
		t.Fatalf("index commit: %q", got)
	}
	if got := gitIn(t, dir, "show", "--name-only", "--format=%an|%s", "HEAD~1"); got != "alpha|Edit a (trail:run)\n\nwiki/a.md" {
		t.Fatalf("agent commit: %q", got)
	}
	if status := gitIn(t, dir, "status", "--porcelain"); status != "A  wiki/staged.md" {
		t.Fatalf("status after commits: %q", status)
	}
}

func TestListAndLinksLogThePagesTheyReturn(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "wiki/agent-memory.md", "x\n")
	put(t, dir, "trails/run.md", "x\n")
	put(t, dir, "sources/survey.md", "x\n")
	wiki := &countingWiki{answers: map[string]string{
		"exec": `{"text":"ls","enrichedPaths":[
			{"path":"wiki","type":"directory","mostRecentMd":{"path":"wiki/index.md"},"templates_available":[{"path":"wiki/.ok/templates/page.md"}]},
			{"path":"wiki/agent-memory.md","title":"Agent memory"},
			{"path":"wiki/ai","type":"directory"},
			{"path":"wiki/index.md","title":"Index"}]}`,
		"links": `{"backlinks":[{"source":"trails/run"},{"source":"wiki/index"}],
			"forwardLinks":[{"docName":"sources/survey"},{"docName":"wiki/gone"}]}`,
	}}
	h := newHarnessWith(t, wiki, dir)
	if _, isErr := h.call(t, "reader", "wiki_list", map[string]any{"folder": "wiki"}); isErr {
		t.Fatal("list failed")
	}
	if _, isErr := h.call(t, "reader", "wiki_links", map[string]any{"kinds": []string{"backlinks", "forward"}, "path": "wiki/agent-memory.md"}); isErr {
		t.Fatal("links failed")
	}
	entries, err := h.reads.Recent(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range entries {
		got[e.Tool] = e.Query + " -> " + strings.Join(e.Returned, ",")
	}
	if got["wiki_list"] != "wiki -> wiki/agent-memory.md" {
		t.Errorf("list row: %q", got["wiki_list"])
	}
	if got["wiki_links"] != "backlinks,forward wiki/agent-memory.md -> trails/run.md,sources/survey.md" {
		t.Errorf("links row: %q", got["wiki_links"])
	}
}
