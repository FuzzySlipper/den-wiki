package facade

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// committer makes den-wiki's content commits. OpenKnowledge never commits to
// the content repo, so every write through the facade ends here: the files
// the write changed are committed with the calling agent as author and
// "<summary> (trail:<slug>)" as subject.
type committer struct {
	dir string
	mu  sync.Mutex
	// persistWait bounds how long to wait for OK to put a write on disk.
	persistWait time.Duration
}

// Commit is the commit a write produced.
type Commit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
}

// identityName must stay simple enough for an email local part.
var nonIdent = regexp.MustCompile(`[^a-z0-9._-]+`)

func authorEmail(agent string) string {
	local := strings.Trim(nonIdent.ReplaceAllString(strings.ToLower(agent), "-"), "-.")
	if local == "" {
		local = "agent"
	}
	return local + "@den-wiki.local"
}

func authorFor(agent string) string { return fmt.Sprintf("%s <%s>", agent, authorEmail(agent)) }

func (c *committer) git(ctx context.Context, args ...string) (string, error) {
	return c.gitEnv(ctx, nil, args...)
}

func (c *committer) gitEnv(ctx context.Context, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{
		"-C", c.dir, "-c", "user.name=den-wiki", "-c", "user.email=den-wiki@den-wiki.local", "-c", "commit.gpgsign=false",
	}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// commit commits exactly paths (content-root-relative; a path missing from
// disk is committed as a deletion) on top of HEAD, as agent. It builds the
// commit in a temporary index, so nothing else staged or changed in the
// working tree is swept in. It returns nil when the paths hold no change.
func (c *committer) commit(ctx context.Context, agent, subject string, paths []string) (*Commit, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(paths) == 0 {
		return nil, nil
	}
	tmp, err := os.MkdirTemp("", "den-wiki-index-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	idx := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}
	head, err := c.git(ctx, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		head = ""
	}
	if head != "" {
		_, err = c.gitEnv(ctx, idx, "read-tree", head)
	} else {
		_, err = c.gitEnv(ctx, idx, "read-tree", "--empty")
	}
	if err != nil {
		return nil, err
	}
	var present, gone []string
	for _, p := range paths {
		if c.exists(p) {
			present = append(present, p)
		} else {
			gone = append(gone, p)
		}
	}
	if len(present) > 0 {
		if _, err := c.gitEnv(ctx, idx, append([]string{"add", "-A", "--"}, present...)...); err != nil {
			return nil, err
		}
	}
	if len(gone) > 0 {
		if _, err := c.gitEnv(ctx, idx, append([]string{"update-index", "--force-remove", "--"}, gone...)...); err != nil {
			return nil, err
		}
	}
	tree, err := c.gitEnv(ctx, idx, "write-tree")
	if err != nil {
		return nil, err
	}
	if head != "" {
		if headTree, _ := c.git(ctx, "rev-parse", head+"^{tree}"); headTree == tree {
			return nil, nil // nothing to commit
		}
	}
	author := []string{"GIT_AUTHOR_NAME=" + agent, "GIT_AUTHOR_EMAIL=" + authorEmail(agent)}
	args := []string{"commit-tree", tree, "-m", subject}
	if head != "" {
		args = append(args, "-p", head)
	}
	sha, err := c.gitEnv(ctx, author, args...)
	if err != nil {
		return nil, err
	}
	// Compare-and-swap: fail rather than drop a commit made meanwhile.
	if _, err := c.git(ctx, "update-ref", "-m", "den-wiki: "+subject, "HEAD", sha, head); err != nil {
		return nil, err
	}
	// Bring the real index in line with the new HEAD for these paths.
	_, _ = c.git(ctx, append([]string{"reset", "-q", "--"}, paths...)...)
	c.commitIndexesLocked(ctx)
	return &Commit{SHA: sha, Subject: subject, Author: authorFor(agent)}, nil
}

// commitIndexesLocked commits index.md files OK regenerated, as their own
// untagged commit (the commit checks exempt generated indexes).
func (c *committer) commitIndexesLocked(ctx context.Context) {
	out, err := c.git(ctx, "status", "--porcelain", "--untracked-files=all", "--", ":(glob)index.md", ":(glob)**/index.md")
	if err != nil || out == "" {
		return
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) > 3 {
			paths = append(paths, strings.Trim(line[3:], `"`))
		}
	}
	args := append([]string{"--"}, paths...)
	if _, err := c.git(ctx, append([]string{"add", "-A"}, args...)...); err != nil {
		return
	}
	_, _ = c.git(ctx, append([]string{"commit", "-q", "--no-verify", "--author", "den-wiki <den-wiki@den-wiki.local>", "-m", "Regenerate indexes"}, args...)...)
}

// waitFor polls until cond holds or the wait runs out. OK acknowledges a
// write before the file is necessarily on disk.
func (c *committer) waitFor(ctx context.Context, cond func() bool) error {
	wait := c.persistWait
	if wait == 0 {
		wait = 10 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		if cond() {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("OpenKnowledge accepted the change but it did not reach the content directory in time; nothing was committed")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (c *committer) abs(rel string) string { return filepath.Join(c.dir, filepath.FromSlash(rel)) }

func (c *committer) read(rel string) ([]byte, bool) {
	b, err := os.ReadFile(c.abs(rel))
	return b, err == nil
}

func (c *committer) exists(rel string) bool {
	_, err := os.Stat(c.abs(rel))
	return err == nil
}
