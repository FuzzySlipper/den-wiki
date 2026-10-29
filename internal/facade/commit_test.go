package facade

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=seed", "-c", "user.email=seed@test", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func put(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCommitterCommitsOnlyItsPaths(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	put(t, dir, "wiki/a.md", "a\n")
	put(t, dir, "wiki/b.md", "b\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "seed")
	c := &committer{dir: dir}
	ctx := context.Background()

	// Another agent's work in progress, staged and unstaged, stays out.
	put(t, dir, "wiki/b.md", "b edited by someone else\n")
	put(t, dir, "wiki/staged.md", "staged\n")
	gitIn(t, dir, "add", "wiki/staged.md")
	put(t, dir, "wiki/a.md", "a v2\n")
	cm, err := c.commit(ctx, "Alpha Agent", "Edit a (trail:run)", []string{"wiki/a.md"})
	if err != nil || cm == nil {
		t.Fatalf("commit: %v %v", cm, err)
	}
	if got := gitIn(t, dir, "show", "--name-only", "--format=%an <%ae>|%s", "HEAD"); got != "Alpha Agent <alpha-agent@den-wiki.local>|Edit a (trail:run)\n\nwiki/a.md" {
		t.Fatalf("HEAD: %q", got)
	}
	status := gitIn(t, dir, "status", "--porcelain")
	if !strings.Contains(status, "A  wiki/staged.md") || !strings.Contains(status, "M wiki/b.md") || strings.Contains(status, "M  wiki/b.md") || strings.Contains(status, "wiki/a.md") {
		t.Fatalf("other changes disturbed or a.md left dirty:\n%s", status)
	}

	// A move commits the deletion and the new file together.
	if err := os.Rename(filepath.Join(dir, "wiki/a.md"), filepath.Join(dir, "wiki/moved.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.commit(ctx, "beta", "Move a (trail:run)", []string{"wiki/a.md", "wiki/moved.md"}); err != nil {
		t.Fatal(err)
	}
	if got := gitIn(t, dir, "show", "--no-renames", "--name-status", "--format=%an", "HEAD"); !strings.Contains(got, "D\twiki/a.md") || !strings.Contains(got, "A\twiki/moved.md") || !strings.HasPrefix(got, "beta") {
		t.Fatalf("move commit:\n%s", got)
	}

	// No change, no commit.
	head := gitIn(t, dir, "rev-parse", "HEAD")
	if cm, err := c.commit(ctx, "beta", "Nothing (trail:run)", []string{"wiki/moved.md"}); err != nil || cm != nil {
		t.Fatalf("no-op commit: %v %v", cm, err)
	}
	if gitIn(t, dir, "rev-parse", "HEAD") != head {
		t.Fatal("HEAD moved on a no-op")
	}
}

func TestDocPathValidation(t *testing.T) {
	good := map[string]string{"wiki/agent-memory.md": "wiki/agent-memory", "/trails/run.md": "trails/run", "sources/a/b.md": "sources/a/b"}
	for in, doc := range good {
		if _, d, err := docPath(in); err != nil || d != doc {
			t.Errorf("docPath(%q) = %q, %v", in, d, err)
		}
	}
	for _, bad := range []string{"wiki/a", "notes/a.md", "wiki/../x.md", "wiki/.ok/templates/page.md", "wiki/index.md", "wiki/a b.md", "wiki/a;rm.md", "wiki//a.md"} {
		if _, _, err := docPath(bad); err == nil {
			t.Errorf("docPath(%q) accepted", bad)
		}
	}
}
