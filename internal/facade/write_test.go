package facade

import (
	"context"
	"encoding/json"
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

	c := &Committer{dir: dir}
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

func TestContentOnlyReadSkipsOpenKnowledge(t *testing.T) {
	dir := t.TempDir()
	page := "---\ntitle: Agent memory\n---\n\nMemory helps.\n"
	put(t, dir, "wiki/agent-memory.md", page)
	wiki := &countingWiki{}
	h := newHarnessWith(t, wiki, dir)
	sc, isErr := h.call(t, "reader", "wiki_read", map[string]any{"path": "wiki/agent-memory.md", "content_only": true})
	if isErr {
		t.Fatalf("read: %v", sc)
	}
	if len(sc) != 2 || sc["path"] != "wiki/agent-memory.md" || sc["content"] != page {
		t.Errorf("content-only read returned %v", sc)
	}
	if wiki.count() != 0 {
		t.Errorf("content-only read called OpenKnowledge %d times", wiki.count())
	}
	entries, err := h.reads.Recent(context.Background(), 10)
	if err != nil || len(entries) != 1 || entries[0].Tool != "wiki_read" || len(entries[0].Opened) != 1 {
		t.Errorf("read log %+v %v", entries, err)
	}
	if sc, isErr := h.call(t, "reader", "wiki_read", map[string]any{"path": "wiki/missing.md", "content_only": true}); !isErr || errorCode(sc) != "not_found" {
		t.Errorf("missing page: %v", sc)
	}
}

func TestGrepAndSectionReadSkipOpenKnowledge(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "taxonomy.yml", "version: 1\ntopics:\n  - {name: projects, description: p}\n")
	put(t, dir, "wiki/combat.md", "---\ntitle: Combat\ntopic: projects\n---\n\n## Stamina\n\nStamina drains.\n\n## Other\n\nNothing.\n")
	put(t, dir, "wiki/elsewhere.md", "---\ntitle: Elsewhere\n---\n\nStamina too.\n")
	wiki := &countingWiki{}
	h := newHarnessWith(t, wiki, dir)

	sc, isErr := h.call(t, "reader", "wiki_grep", map[string]any{"pattern": "drains", "topic": "projects", "context": 1})
	if isErr {
		t.Fatalf("grep: %v", sc)
	}
	ps, _ := sc["passages"].([]any)
	if len(ps) != 1 || sc["matches"] != float64(1) {
		t.Fatalf("grep result %v", sc)
	}
	p := ps[0].(map[string]any)
	if p["path"] != "wiki/combat.md" || p["heading"] != "Stamina" || len(p["lines"].([]any)) != 3 {
		t.Errorf("passage %v", p)
	}
	// context 0 returns the matching line alone; omitted, it is 2.
	for _, c := range []struct {
		args  map[string]any
		lines int
	}{
		{map[string]any{"pattern": "drains", "path": "wiki/combat.md", "context": 0}, 1},
		{map[string]any{"pattern": "drains", "path": "wiki/combat.md"}, 5},
	} {
		sc, isErr := h.call(t, "reader", "wiki_grep", c.args)
		ps, _ := sc["passages"].([]any)
		if isErr || len(ps) != 1 || len(ps[0].(map[string]any)["lines"].([]any)) != c.lines {
			t.Errorf("%v: want %d lines: %v", c.args, c.lines, sc)
		}
	}
	for args, code := range map[string]string{
		`{"pattern":"x","path":"wiki/a.md","zone":"wiki"}`: "invalid_arguments",
		`{"pattern":"(","regex":true}`:                     "invalid_arguments",
		`{"pattern":"x","topic":"cooking"}`:                "not_found",
	} {
		var m map[string]any
		_ = json.Unmarshal([]byte(args), &m)
		if sc, isErr := h.call(t, "reader", "wiki_grep", m); !isErr || errorCode(sc) != code {
			t.Errorf("%s: %v", args, sc)
		}
	}

	sc, isErr = h.call(t, "reader", "wiki_read", map[string]any{"path": "wiki/combat.md", "section": "stamina"})
	if isErr || sc["section"] != "Stamina" || sc["content"] != "## Stamina\n\nStamina drains.\n\n" {
		t.Errorf("section read: %v", sc)
	}
	sc, isErr = h.call(t, "reader", "wiki_read", map[string]any{"path": "wiki/combat.md", "section": "Missing"})
	if !isErr || errorCode(sc) != "not_found" || !strings.Contains(sc["error"].(map[string]any)["message"].(string), "Stamina; Other") {
		t.Errorf("missing section: %v", sc)
	}
	if wiki.count() != 0 {
		t.Errorf("grep and section reads called OpenKnowledge %d times", wiki.count())
	}
	entries, _ := h.reads.Recent(context.Background(), 10)
	var logged bool
	for _, e := range entries {
		if e.Tool == "wiki_grep" && e.Query == "drains" && len(e.Returned) == 1 && e.Returned[0] == "wiki/combat.md" {
			logged = true
		}
	}
	if !logged {
		t.Errorf("grep not in the read log: %+v", entries)
	}
}
