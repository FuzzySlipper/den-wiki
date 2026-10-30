package facade

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sort"
	"strings"
	"time"
)

// UI commits. OpenKnowledge's web UI writes straight to the content
// directory and never commits, and its visual editor re-saves a page in its
// own Markdown formatting when someone only views it. The reconciler finds
// those changes and commits them, so they reach history and the backup.
//
// Subjects carry a tag in place of (trail:<slug>): "(ui)" for an edit and
// "(ui:format)" for a change that only moved whitespace. Lint's commit
// checks recognise both (lint.UITag).

// UIOptions configure the reconciler.
type UIOptions struct {
	// Author is the git author of UI commits.
	Author string
	// Interval is how often to look for changes.
	Interval time.Duration
	// Quiet is how long a file must go unchanged before it is committed, so
	// a page being typed into is not committed on every save.
	Quiet time.Duration
	// Now is the clock; default time.Now.
	Now func() time.Time
}

// UIResult is what one reconcile pass did.
type UIResult struct {
	Edits    []*Commit `json:"edits,omitempty"`
	Format   *Commit   `json:"format,omitempty"`
	Retried  []*Commit `json:"retried,omitempty"`
	Restored []string  `json:"restored,omitempty"`
	Waiting  []string  `json:"waiting,omitempty"`
}

// RunUICommits reconciles every opts.Interval until ctx ends.
func (c *Committer) RunUICommits(ctx context.Context, opts UIOptions, logger *slog.Logger) {
	t := time.NewTicker(opts.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		r, err := c.ReconcileUI(ctx, opts)
		if err != nil {
			logger.Warn("ui_commit_failed", "error", err.Error())
			continue
		}
		for _, cm := range append(append(append([]*Commit{}, r.Retried...), r.Edits...), r.Format) {
			if cm != nil {
				logger.Info("ui_commit", "sha", cm.SHA, "subject", cm.Subject, "author", cm.Author)
			}
		}
		if len(r.Restored) > 0 {
			logger.Info("ui_format_restored", "paths", r.Restored)
		}
	}
}

// ReconcileUI commits the zone files that changed outside a facade write.
// It holds the write lock, so it never runs inside a facade write and a
// facade write never sweeps up its changes.
//
//   - A path a facade write left uncommitted is committed under that
//     write's agent and subject.
//   - A file changed less than Quiet ago is left for a later pass.
//   - A source whose change is whitespace only is restored from HEAD:
//     captures are byte-exact, and their sha256 must keep matching.
//   - Other whitespace-only changes are committed together as one
//     "(ui:format)" commit.
//   - Everything else is committed as one "(ui)" edit.
func (c *Committer) ReconcileUI(ctx context.Context, opts UIOptions) (UIResult, error) {
	var r UIResult
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Author == "" {
		opts.Author = "ui"
	}
	c.writes.Lock()
	defer c.writes.Unlock()
	changed, err := c.changedZoneFiles(ctx)
	if err != nil {
		return r, err
	}
	pending := c.pendingWrites()

	// Retry facade writes first, grouped by the write they came from.
	dirty := map[string]bool{}
	for _, p := range changed {
		dirty[p] = true
	}
	retry := map[pendingWrite][]string{}
	var order []pendingWrite
	var gone []string
	for p, w := range pending {
		if !dirty[p] {
			gone = append(gone, p) // committed meanwhile, or never reached disk
			continue
		}
		if retry[w] == nil {
			order = append(order, w)
		}
		retry[w] = append(retry[w], p)
		delete(dirty, p)
	}
	c.forget(gone)
	sort.Slice(order, func(i, j int) bool { return order[i].subject < order[j].subject })
	for _, w := range order {
		paths := retry[w]
		sort.Strings(paths)
		cm, err := c.commit(ctx, w.agent, w.subject, paths)
		if err != nil {
			return r, err
		}
		c.forget(paths)
		if cm != nil {
			r.Retried = append(r.Retried, cm)
		}
	}

	cutoff := opts.Now().Add(-opts.Quiet)
	var edits, format []string
	for _, p := range changed {
		if !dirty[p] {
			continue
		}
		if fi, err := os.Stat(c.abs(p)); err == nil && fi.ModTime().After(cutoff) {
			r.Waiting = append(r.Waiting, p)
			continue
		}
		if !c.whitespaceOnly(ctx, p) {
			edits = append(edits, p)
			continue
		}
		if strings.HasPrefix(p, "sources/") {
			if _, err := c.git(ctx, "checkout", "HEAD", "--", p); err != nil {
				return r, err
			}
			r.Restored = append(r.Restored, p)
			continue
		}
		format = append(format, p)
	}
	if len(edits) > 0 {
		cm, err := c.commit(ctx, opts.Author, "Edit in the OpenKnowledge UI: "+listPaths(edits)+" (ui)", edits)
		if err != nil {
			return r, err
		}
		if cm != nil {
			r.Edits = append(r.Edits, cm)
		}
	}
	if len(format) > 0 {
		cm, err := c.commit(ctx, opts.Author, "Formatting only, re-saved by the OpenKnowledge editor: "+listPaths(format)+" (ui:format)", format)
		if err != nil {
			return r, err
		}
		r.Format = cm
	}
	return r, nil
}

// changedZoneFiles lists the content-root-relative files under sources/,
// trails/ and wiki/ that differ from HEAD, untracked ones included and
// generated index.md files (which commitIndexesLocked handles) left out.
func (c *Committer) changedZoneFiles(ctx context.Context) ([]string, error) {
	out, err := c.gitRaw(ctx, nil, "status", "--porcelain", "-z", "--no-renames", "--untracked-files=all", "--", "sources", "trails", "wiki")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range strings.Split(string(out), "\x00") {
		if len(entry) <= 3 {
			continue
		}
		p := entry[3:]
		if p == indexFile || strings.HasSuffix(p, "/"+indexFile) {
			continue
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths, nil
}

const indexFile = "index.md"

// whitespaceOnly reports whether a tracked file still on disk differs from
// HEAD only in blank lines and trailing whitespace, which is all OK's editor
// has been seen to change when it re-saves a page.
func (c *Committer) whitespaceOnly(ctx context.Context, rel string) bool {
	disk, ok := c.read(rel)
	if !ok {
		return false
	}
	head, err := c.gitRaw(ctx, nil, "show", "HEAD:"+rel)
	if err != nil {
		return false // new file
	}
	return slices.Equal(contentLines(head), contentLines(disk))
}

// contentLines returns the non-blank lines of b, trailing whitespace trimmed.
func contentLines(b []byte) []string {
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if line = strings.TrimRight(line, " \t\r"); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// listPaths names up to three paths for a commit subject.
func listPaths(paths []string) string {
	if len(paths) <= 3 {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(paths[:3], ", "), len(paths)-3)
}
