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
	// A whitespace-only source change is recorded, not reverted: it may be
	// a person's edit. Lint reports the changed capture.
	if r.Format == nil || r.Format.Subject != "Formatting only, re-saved by the OpenKnowledge editor: sources/capture.md, wiki/viewed.md (ui:format)" {
		t.Errorf("format: %+v", r.Format)
	}
	if strings.Join(r.Waiting, ",") != "wiki/typing.md" {
		t.Errorf("waiting %v", r.Waiting)
	}
	if got := read(t, dir, "sources/capture.md"); got != "---\nurl: x\n---\nCaptured.\n" {
		t.Errorf("source edit not kept: %q", got)
	}
	for sha, files := range map[string]string{r.Retried[0].SHA: "wiki/failed.md", r.Edits[0].SHA: "wiki/edited.md\nwiki/new-page.md", r.Format.SHA: "sources/capture.md\nwiki/viewed.md"} {
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

// A facade write that has not reached disk when a pass runs keeps its
// attribution until it lands, and a later facade commit of the same path
// supersedes it.
func TestReconcileUIKeepsPendingWritesUntilTheyLand(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	put(t, dir, "wiki/slow.md", "v1\n")
	put(t, dir, "wiki/other.md", "v1\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "seed")
	c := NewCommitter(dir)
	ctx := context.Background()
	opts := UIOptions{Quiet: time.Minute}

	// Timed out: one write to an existing page, one creating a page.
	c.remember("alpha", "Edit slow (trail:run)", []string{"wiki/slow.md"})
	c.remember("alpha", "Create late (trail:run)", []string{"wiki/late.md"})
	if r, err := c.ReconcileUI(ctx, opts); err != nil || len(r.Retried)+len(r.Edits) != 0 {
		t.Fatalf("pass before the writes land: %+v %v", r, err)
	}
	if len(c.pendingWrites()) != 2 {
		t.Fatalf("pending dropped early: %v", c.pendingWrites())
	}
	put(t, dir, "wiki/slow.md", "v2\n")
	put(t, dir, "wiki/late.md", "late\n")
	age(t, dir, "wiki/slow.md", 5*time.Minute)
	age(t, dir, "wiki/late.md", 5*time.Minute)
	r, err := c.ReconcileUI(ctx, opts)
	if err != nil || len(r.Retried) != 2 || len(r.Edits) != 0 {
		t.Fatalf("pass after they land: %+v %v", r, err)
	}
	for _, cm := range r.Retried {
		if cm.Author != "alpha <alpha@den-wiki.local>" || !strings.HasSuffix(cm.Subject, "(trail:run)") {
			t.Errorf("retried as %+v", cm)
		}
	}

	// A later facade commit of the path settles an earlier failed write.
	c.remember("beta", "Edit other (trail:run)", []string{"wiki/other.md"})
	put(t, dir, "wiki/other.md", "v3\n")
	if _, err := c.settle(ctx, nil, "gamma", "Edit other again (trail:run)", []string{"wiki/other.md"}); err != nil {
		t.Fatal(err)
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
