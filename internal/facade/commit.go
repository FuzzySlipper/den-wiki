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
	// writes serialises whole write operations: the OK mutation, the wait
	// for it to reach disk and its commit. Without it a second agent's write
	// to the same file (or a move rewriting it) could land between the first
	// agent's wait and commit, and be committed under the first agent.
	writes sync.Mutex
	mu     sync.Mutex
	// persistWait bounds how long to wait for OK to put a write on disk.
	persistWait time.Duration
}

// Commit is the commit a write produced.
type Commit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	// IndexCommitError says why OK's regenerated indexes could not be
	// committed after this commit; the commit itself stands.
	IndexCommitError string `json:"index_commit_error,omitempty"`
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
	out, err := c.gitRaw(ctx, env, args...)
	return strings.TrimSpace(string(out)), err
}

// gitRaw returns git's output byte for byte, for formats where leading
// whitespace matters.
func (c *committer) gitRaw(ctx context.Context, env []string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{
		"-C", c.dir, "-c", "user.name=den-wiki", "-c", "user.email=den-wiki@den-wiki.local", "-c", "commit.gpgsign=false",
	}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
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
	cm := &Commit{SHA: sha, Subject: subject, Author: authorFor(agent)}
	if err := c.commitIndexesLocked(ctx); err != nil {
		cm.IndexCommitError = err.Error()
	}
	return cm, nil
}

// commitIndexesLocked commits index.md files OK regenerated, as their own
// untagged commit (the commit checks exempt generated indexes). Only those
// paths are committed; anything else staged stays staged.
func (c *committer) commitIndexesLocked(ctx context.Context) error {
	out, err := c.gitRaw(ctx, nil, "status", "--porcelain", "-z", "--no-renames", "--untracked-files=all", "--", ":(glob)index.md", ":(glob)**/index.md")
	if err != nil {
		return err
	}
	var paths []string
	// -z entries are "XY <path>" NUL-terminated, with the path unquoted.
	for _, entry := range strings.Split(string(out), "\x00") {
		if len(entry) > 3 {
			paths = append(paths, entry[3:])
		}
	}
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"--"}, paths...)
	if _, err := c.git(ctx, append([]string{"add", "-A"}, args...)...); err != nil {
		return err
	}
	_, err = c.git(ctx, append([]string{"commit", "-q", "--no-verify", "--author", "den-wiki <den-wiki@den-wiki.local>", "-m", "Regenerate indexes"}, args...)...)
	return err
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
