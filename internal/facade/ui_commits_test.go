package facade

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func age(t *testing.T, dir, rel string, d time.Duration) {
	t.Helper()
	old := time.Now().Add(-d)
	if err := os.Chtimes(filepath.Join(dir, rel), old, old); err != nil {
		t.Fatal(err)
	}
}

func TestReconcileUI(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	put(t, dir, "wiki/viewed.md", "---\ntitle: Viewed\n---\n\nText.[^1]\n\n[^1]: one\n[^2]: two\n")
	put(t, dir, "wiki/edited.md", "Before.\n")
	put(t, dir, "wiki/typing.md", "Draft.\n")
	put(t, dir, "wiki/failed.md", "v1\n")
	put(t, dir, "sources/capture.md", "---\nurl: x\n---\n\nCaptured.\n")
	put(t, dir, "trails/run.md", "## Sources\n\nNone.\n")
	put(t, dir, "taxonomy.yml", "version: 1\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "seed")
	c := NewCommitter(dir)
	ctx := context.Background()

	// OK's editor re-saved a page and a source when they were viewed.
	put(t, dir, "wiki/viewed.md", "---\ntitle: Viewed\n---\nText.[^1]  \n\n[^1]: one\n\n[^2]: two\n")
	put(t, dir, "sources/capture.md", "---\nurl: x\n---\nCaptured.\n")
	// Someone edited a page, and created one.
	put(t, dir, "wiki/edited.md", "After.\n")
	put(t, dir, "wiki/new-page.md", "New.\n")
	// Someone is still typing into another.
	put(t, dir, "wiki/typing.md", "Draft, more.\n")
	// A facade write reached disk after its commit gave up.
	put(t, dir, "wiki/failed.md", "v2\n")
	c.remember("alpha", "Write wiki/failed.md (trail:run)", []string{"wiki/failed.md"})
	// Outside the zones; not the reconciler's business.
	put(t, dir, "taxonomy.yml", "version: 1\n# note\n")
	for _, p := range []string{"wiki/viewed.md", "sources/capture.md", "wiki/edited.md", "wiki/new-page.md", "wiki/failed.md"} {
		age(t, dir, p, 5*time.Minute)
	}

	r, err := c.ReconcileUI(ctx, UIOptions{Author: "ui", Quiet: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Retried) != 1 || r.Retried[0].Author != "alpha <alpha@den-wiki.local>" || r.Retried[0].Subject != "Write wiki/failed.md (trail:run)" {
		t.Errorf("retried: %+v", r.Retried)
	}
	if len(r.Edits) != 1 || r.Edits[0].Subject != "Edit in the OpenKnowledge UI: wiki/edited.md, wiki/new-page.md (ui)" || r.Edits[0].Author != "ui <ui@den-wiki.local>" {
		t.Errorf("edits: %+v", r.Edits)
	}
	if r.Format == nil || r.Format.Subject != "Formatting only, re-saved by the OpenKnowledge editor: wiki/viewed.md (ui:format)" {
		t.Errorf("format: %+v", r.Format)
	}
	if strings.Join(r.Restored, ",") != "sources/capture.md" || strings.Join(r.Waiting, ",") != "wiki/typing.md" {
		t.Errorf("restored %v waiting %v", r.Restored, r.Waiting)
	}
	if got := gitIn(t, dir, "show", "HEAD:sources/capture.md"); got != strings.TrimSpace(read(t, dir, "sources/capture.md")) {
		t.Errorf("source not restored: %q", read(t, dir, "sources/capture.md"))
	}
	for sha, files := range map[string]string{r.Retried[0].SHA: "wiki/failed.md", r.Edits[0].SHA: "wiki/edited.md\nwiki/new-page.md", r.Format.SHA: "wiki/viewed.md"} {
		if got := gitIn(t, dir, "show", "--name-only", "--format=", sha); got != files {
			t.Errorf("commit %s has %q, want %q", sha[:7], got, files)
		}
	}
	if st := gitIn(t, dir, "status", "--porcelain"); st != "M taxonomy.yml\n M wiki/typing.md" {
		t.Errorf("status:\n%s", st)
	}

	// Once typing stops, the next pass commits it; a pass with nothing to
	// do commits nothing.
	age(t, dir, "wiki/typing.md", 5*time.Minute)
	r, err = c.ReconcileUI(ctx, UIOptions{Quiet: time.Minute})
	if err != nil || len(r.Edits) != 1 || r.Edits[0].Subject != "Edit in the OpenKnowledge UI: wiki/typing.md (ui)" {
		t.Fatalf("second pass: %+v %v", r, err)
	}
	head := gitIn(t, dir, "rev-parse", "HEAD")
	if r, err = c.ReconcileUI(ctx, UIOptions{Quiet: time.Minute}); err != nil || len(r.Edits)+len(r.Retried) != 0 || r.Format != nil {
		t.Fatalf("idle pass: %+v %v", r, err)
	}
	if gitIn(t, dir, "rev-parse", "HEAD") != head {
		t.Fatal("idle pass moved HEAD")
	}
	if len(c.pendingWrites()) != 0 {
		t.Errorf("pending left: %v", c.pendingWrites())
	}
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestListPaths(t *testing.T) {
	if got := listPaths([]string{"a", "b", "c", "d", "e"}); got != "a, b, c and 2 more" {
		t.Errorf("%q", got)
	}
}
