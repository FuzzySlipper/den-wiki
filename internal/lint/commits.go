package lint

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// TrailTag matches the (trail:<slug>) commit-subject tag.
var TrailTag = regexp.MustCompile(`\(trail:([a-z0-9]+(?:-[a-z0-9]+)*)\)`)

type commit struct {
	sha, subject string
	// changes are "<status>\t<path>" from --name-status.
	changes []fileChange
}

type fileChange struct {
	status, path string
}

// checkCommits reports contract findings for every commit in rangeSpec:
// wiki/ changes without a trail tag, tags naming missing trails, edited
// sources, and trails that lost lines.
func (l *linter) checkCommits(rangeSpec string) ([]Finding, error) {
	if strings.HasPrefix(rangeSpec, "-") {
		return nil, fmt.Errorf("commit range %q must not start with -", rangeSpec)
	}
	prefix, err := l.git("rev-parse", "--show-prefix")
	if err != nil {
		return nil, err
	}
	prefix = strings.TrimSpace(prefix)
	commits, err := l.logNameStatus(rangeSpec)
	if err != nil {
		return nil, err
	}
	shrunk, err := l.trailDeletions(rangeSpec, prefix)
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, c := range commits {
		wikiPaths := []string{}
		for _, ch := range c.changes {
			rel, ok := strings.CutPrefix(ch.path, prefix)
			if !ok {
				continue
			}
			if !isZoneDocument(rel) {
				// Generated indexes, .ok/ rules and templates, and assets are
				// not pages, sources or trails, so the zone rules skip them.
				continue
			}
			switch {
			case strings.HasPrefix(rel, zoneWiki+"/"):
				wikiPaths = append(wikiPaths, rel)
			case strings.HasPrefix(rel, zoneSources+"/") && ch.status == "M":
				out = append(out, commitFinding(codeCommitSourceEdit, rel, c, "commit modifies an existing source; capture a new file with supersedes instead"))
			case strings.HasPrefix(rel, zoneTrails+"/") && ch.status == "D":
				out = append(out, commitFinding(codeCommitTrailEdit, rel, c, "commit deletes a trail; trails are append-only"))
			case strings.HasPrefix(rel, zoneTrails+"/") && ch.status == "M" && shrunk[c.sha+"\x00"+ch.path]:
				out = append(out, commitFinding(codeCommitTrailEdit, rel, c, "commit removes lines from a trail; trails are append-only"))
			}
		}
		if len(wikiPaths) == 0 {
			continue
		}
		m := TrailTag.FindStringSubmatch(c.subject)
		if m == nil {
			out = append(out, commitFinding(codeCommitTrailTag, "", c, fmt.Sprintf("subject %q changes %s without (trail:<slug>)", c.subject, strings.Join(wikiPaths, ", "))))
			continue
		}
		// The trail has to exist in the commit itself: a later commit that
		// deletes it is reported on its own, and an earlier tag cannot point
		// forward to a trail written afterwards.
		if _, err := l.git("cat-file", "-e", c.sha+":"+prefix+zoneTrails+"/"+m[1]+".md"); err != nil {
			out = append(out, commitFinding(codeCommitTrailFound, "", c, "subject names trail "+quote(m[1])+" but trails/"+m[1]+".md does not exist in that commit"))
		}
	}
	return out, nil
}

// isZoneDocument reports whether a content-root-relative path is a page,
// source or trail: a .md file in a zone that is not a generated index.md and
// is not inside a dot-folder (such as .ok/) or an OpenKnowledge .assets/
// folder. It is the same set of files walkZone checks.
func isZoneDocument(rel string) bool {
	segs := strings.Split(rel, "/")
	if len(segs) < 2 || !isZone(segs[0]) {
		return false
	}
	name := segs[len(segs)-1]
	if name == indexName || !strings.HasSuffix(name, ".md") {
		return false
	}
	for _, s := range segs[1:] {
		if strings.HasPrefix(s, ".") {
			return false
		}
	}
	for _, s := range segs[1 : len(segs)-1] {
		if strings.HasSuffix(s, assetsSuffix) {
			return false
		}
	}
	return true
}

func commitFinding(code, rel string, c commit, msg string) Finding {
	f := newFinding(code, rel, msg)
	f.Commit = c.sha
	return f
}

func (l *linter) git(args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", l.root}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// logNameStatus lists commits in rangeSpec with the files each one changes.
// Paths are relative to the repository top level.
func (l *linter) logNameStatus(rangeSpec string) ([]commit, error) {
	out, err := l.git("log", "--no-renames", "--format=%x1e%H%x1f%s", "--name-status", rangeSpec, "--")
	if err != nil {
		return nil, err
	}
	var commits []commit
	for _, rec := range strings.Split(out, "\x1e") {
		if strings.TrimSpace(rec) == "" {
			continue
		}
		head, rest, _ := strings.Cut(rec, "\n")
		sha, subject, _ := strings.Cut(head, "\x1f")
		c := commit{sha: sha, subject: subject}
		for _, line := range strings.Split(rest, "\n") {
			status, p, ok := strings.Cut(line, "\t")
			if !ok {
				continue
			}
			c.changes = append(c.changes, fileChange{status: status[:1], path: p})
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// trailDeletions returns "<sha>\x00<path>" for every trail file a commit in
// rangeSpec removed lines from.
func (l *linter) trailDeletions(rangeSpec, prefix string) (map[string]bool, error) {
	out, err := l.git("log", "--no-renames", "--format=%x1e%H", "--numstat", rangeSpec, "--", ":/"+prefix+zoneTrails)
	if err != nil {
		return nil, err
	}
	shrunk := map[string]bool{}
	for _, rec := range strings.Split(out, "\x1e") {
		head, rest, _ := strings.Cut(rec, "\n")
		sha := strings.TrimSpace(head)
		for _, line := range strings.Split(rest, "\n") {
			parts := strings.SplitN(line, "\t", 3)
			if len(parts) != 3 {
				continue
			}
			if parts[1] != "0" {
				shrunk[sha+"\x00"+parts[2]] = true
			}
		}
	}
	return shrunk, nil
}
