package lint

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRun(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=lint-test", "-c", "user.email=lint@test", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitAll(t *testing.T, root, subject string) string {
	t.Helper()
	gitRun(t, root, "add", "-A")
	gitRun(t, root, "commit", "-q", "-m", subject)
	return gitRun(t, root, "rev-parse", "HEAD")
}

// newContentRepo makes a git repository whose first commit is the valid
// fixture, placed under subdir (empty for the repository root).
func newContentRepo(t *testing.T, subdir string) (repo, content string) {
	t.Helper()
	repo = t.TempDir()
	gitRun(t, repo, "init", "-q", "-b", "main")
	content = repo
	if subdir != "" {
		content = filepath.Join(repo, subdir)
		if err := os.MkdirAll(content, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	copyInto(t, validFixture, content)
	commitAll(t, repo, "seed content")
	return repo, content
}

func copyInto(t *testing.T, src, dst string) {
	t.Helper()
	out, err := exec.Command("cp", "-r", src+"/.", dst).CombinedOutput()
	if err != nil {
		t.Fatalf("cp: %v\n%s", err, out)
	}
}

func TestCommitRange(t *testing.T) {
	for _, subdir := range []string{"", "content"} {
		t.Run("subdir="+subdir, func(t *testing.T) {
			repo, root := newContentRepo(t, subdir)
			base := gitRun(t, repo, "rev-parse", "HEAD")

			replace(t, root, page, "Provenance on each", "Provenance on every")
			tagged := commitAll(t, repo, "Refine agent memory (trail:agent-memory-research-2026-10-28)")

			replace(t, root, page, "Provenance on every", "Provenance on each")
			untagged := commitAll(t, repo, "Tweak agent memory")

			replace(t, root, page, "Provenance on each", "Provenance on every")
			unknown := commitAll(t, repo, "Tweak again (trail:no-such-run)")

			write(t, root, "wiki/index.md", "# wiki\n\nregenerated\n")
			indexOnly := commitAll(t, repo, "Regenerate index")

			replace(t, root, source, "more\nconsistently", "far more\nconsistently")
			sourceEdit := commitAll(t, repo, "Fix a typo in the source")

			write(t, root, "sources/new-capture.md", read(t, root, source))
			sourceAdd := commitAll(t, repo, "Capture another source")

			write(t, root, trail, read(t, root, trail)+"\nAppended note.\n")
			trailAppend := commitAll(t, repo, "Append to the trail")

			replace(t, root, trail, "None; the wiki had no memory pages.\n", "")
			trailRewrite := commitAll(t, repo, "Tidy the trail")

			remove(t, root, trail)
			trailDelete := commitAll(t, repo, "Drop the trail")

			fs, err := Lint(root, Options{Commits: base + "..HEAD"})
			if err != nil {
				t.Fatal(err)
			}
			byCommit := map[string][]string{}
			for _, f := range fs {
				if f.Commit != "" {
					byCommit[f.Commit] = append(byCommit[f.Commit], f.Code)
				}
			}
			want := map[string][]string{
				untagged:     {"commit.missing_trail_tag"},
				unknown:      {"commit.unknown_trail"},
				sourceEdit:   {"commit.source_edited"},
				trailRewrite: {"commit.trail_rewritten"},
				trailDelete:  {"commit.trail_rewritten"},
			}
			for _, clean := range []string{tagged, indexOnly, sourceAdd, trailAppend} {
				if got := byCommit[clean]; len(got) != 0 {
					t.Errorf("commit %s: unexpected %v", shortSHA(clean), got)
				}
			}
			for sha, codes := range want {
				if strings.Join(byCommit[sha], ",") != strings.Join(codes, ",") {
					t.Errorf("commit %s: got %v, want %v", shortSHA(sha), byCommit[sha], codes)
				}
			}
			if t.Failed() {
				t.Logf("findings:%s", dump(fs))
			}
		})
	}
}

func TestCommitRangeErrors(t *testing.T) {
	if _, err := Lint(validFixture, Options{Commits: "--all"}); err == nil {
		t.Fatal("option-like range accepted")
	}
	_, root := newContentRepo(t, "")
	if _, err := Lint(root, Options{Commits: "no-such-ref..HEAD"}); err == nil {
		t.Fatal("bad range accepted")
	}
}
