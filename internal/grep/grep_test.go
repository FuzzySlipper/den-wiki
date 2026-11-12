package grep

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const combat = `---
title: Combat
topic: rusty-dagger
tags: [stamina]
---

Intro mentions stamina once.

## Foundation

### Stamina and Fatigue

Line one.
Stamina drains on attacks.
Line three.
Stamina recovers at rest.

` + "```text" + `
## Not a heading
stamina in code
` + "```" + `

## Open decisions

Nothing about it here.
`

func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"taxonomy.yml":                    "version: 1\ntopics:\n  - {name: projects, description: p}\n  - {name: rusty-dagger, description: r, parent: projects}\n  - {name: games, description: g}\n",
		"wiki/rusty-dagger/combat.md":     combat,
		"wiki/rusty-dagger.md":            "---\ntitle: Rusty Dagger\ntopic: rusty-dagger\n---\n\nStamina overview.\n",
		"wiki/other.md":                   "---\ntitle: Other\ntopic: projects\n---\n\nStamina elsewhere.\n",
		"wiki/daggerfall.md":              "---\ntitle: Daggerfall\ntopic: games\n---\n\nStamina in the original.\n",
		"wiki/index.md":                   "Stamina in a generated index.\n",
		"wiki/combat.assets/notes.md":     "Stamina in an asset.\n",
		"sources/survey.md":               "---\ntitle: Survey\n---\n\nstamina quote\n",
		"trails/run.md":                   "---\ntype: trail\n---\n\n## Queries and methods\n\nSTAMINA search.\n",
		"wiki/.ok/templates/page.md":      "stamina template\n",
		"elsewhere/outside.md":            "stamina outside the zones\n",
		"wiki/rusty-dagger/long-line.md":  "---\ntitle: Long\n---\n\n" + strings.Repeat("a", 600) + " stamina " + strings.Repeat("b", 600) + "\n",
		"wiki/rusty-dagger/no-stamina.md": "---\ntitle: Quiet\ntopic: rusty-dagger\n---\n\nNothing.\n",
	}
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func paths(r Result) []string {
	var out []string
	for _, p := range r.Passages {
		if len(out) == 0 || out[len(out)-1] != p.Path {
			out = append(out, p.Path)
		}
	}
	return out
}

func TestScopes(t *testing.T) {
	dir := fixture(t)
	for _, c := range []struct {
		name  string
		scope Scope
		want  string
	}{
		{"everything", Scope{}, "wiki/daggerfall.md wiki/other.md wiki/rusty-dagger.md wiki/rusty-dagger/combat.md wiki/rusty-dagger/long-line.md sources/survey.md trails/run.md"},
		{"zone", Scope{Zone: "sources"}, "sources/survey.md"},
		{"folder", Scope{Folder: "wiki/rusty-dagger/"}, "wiki/rusty-dagger/combat.md wiki/rusty-dagger/long-line.md"},
		{"path", Scope{Path: "wiki/other.md"}, "wiki/other.md"},
		// A topic covers its subtopics, by frontmatter rather than folder.
		{"topic", Scope{Topic: "projects"}, "wiki/other.md wiki/rusty-dagger.md wiki/rusty-dagger/combat.md"},
		{"subtopic", Scope{Topic: "rusty-dagger"}, "wiki/rusty-dagger.md wiki/rusty-dagger/combat.md"},
	} {
		r, err := Search(dir, c.scope, Options{Pattern: "stamina"})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := strings.Join(paths(r), " "); got != c.want {
			t.Errorf("%s: %s\nwant %s", c.name, got, c.want)
		}
	}
	for _, c := range []struct {
		scope Scope
		err   error
	}{
		{Scope{Path: "wiki/missing.md"}, ErrNotFound},
		{Scope{Path: "../etc/passwd.md"}, ErrInvalid},
		{Scope{Path: "elsewhere/outside.md"}, ErrInvalid},
		{Scope{Folder: "wiki/nope"}, ErrNotFound},
		{Scope{Zone: "media"}, ErrInvalid},
		{Scope{Topic: "cooking"}, ErrNotFound},
		{Scope{Path: "wiki/other.md", Zone: "wiki"}, ErrInvalid},
	} {
		if _, err := Search(dir, c.scope, Options{Pattern: "stamina"}); !errors.Is(err, c.err) {
			t.Errorf("%+v: err %v, want %v", c.scope, err, c.err)
		}
	}
}

func TestPassagesCarryContextAndHeadings(t *testing.T) {
	dir := fixture(t)
	r, err := Search(dir, Scope{Path: "wiki/rusty-dagger/combat.md"}, Options{Pattern: "stamina", Context: 1})
	if err != nil {
		t.Fatal(err)
	}
	// Six matching lines: the frontmatter tags line and the intro (one
	// passage, their windows touch); the heading line and two body lines
	// (one passage); the line in the code fence.
	if r.Matches != 6 || len(r.Passages) != 3 || r.Truncated {
		t.Fatalf("matches %d passages %d truncated %v: %+v", r.Matches, len(r.Passages), r.Truncated, r.Passages)
	}
	if p := r.Passages[0]; p.Heading != "" || len(p.Hits) != 2 || p.Hits[0] != 4 || p.Hits[1] != 7 {
		t.Errorf("first passage %+v", p)
	}
	p := r.Passages[1]
	if p.Heading != "Foundation › Stamina and Fatigue" || len(p.Hits) != 3 || p.Hits[0] != 11 || p.Hits[2] != 16 {
		t.Errorf("passage %+v", p)
	}
	if len(p.Lines) != 8 || p.Lines[0].N != 10 || p.Lines[3].Text != "Line one." || p.Lines[7].N != 17 {
		t.Errorf("lines %+v", p.Lines)
	}
	// The fenced "## Not a heading" does not change the heading.
	if h := r.Passages[2].Heading; h != "Foundation › Stamina and Fatigue" {
		t.Errorf("fenced heading counted: %q", h)
	}
}

func TestOptionsAndLimits(t *testing.T) {
	dir := fixture(t)
	r, _ := Search(dir, Scope{Path: "trails/run.md"}, Options{Pattern: "stamina", CaseSensitive: true})
	if r.Matches != 0 || r.Searched != 1 {
		t.Errorf("case-sensitive: %+v", r)
	}
	r, _ = Search(dir, Scope{Path: "wiki/rusty-dagger/combat.md"}, Options{Pattern: `drains|recovers`, Regex: true})
	if r.Matches != 2 {
		t.Errorf("regex: %d matches", r.Matches)
	}
	r, _ = Search(dir, Scope{Path: "wiki/rusty-dagger/combat.md"}, Options{Pattern: "drains|recovers"})
	if r.Matches != 0 {
		t.Errorf("literal pattern matched as a regex: %d", r.Matches)
	}
	if _, err := Search(dir, Scope{}, Options{Pattern: "("}); err != nil {
		t.Errorf("literal ( : %v", err)
	}
	if _, err := Search(dir, Scope{}, Options{Pattern: "(", Regex: true}); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad regex: %v", err)
	}
	if _, err := Search(dir, Scope{}, Options{Pattern: "  "}); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty pattern: %v", err)
	}
	r, _ = Search(dir, Scope{}, Options{Pattern: "stamina", PerFile: 1, Max: 3})
	if r.Matches != 3 || r.Files != 3 || !r.Truncated {
		t.Errorf("limits: matches %d files %d truncated %v", r.Matches, r.Files, r.Truncated)
	}
	r, _ = Search(dir, Scope{Path: "wiki/rusty-dagger/long-line.md"}, Options{Pattern: "stamina"})
	l := r.Passages[0].Lines[0].Text
	if n := len([]rune(l)); n > maxLine+2 || !strings.Contains(l, "stamina") || !strings.HasPrefix(l, "…") || !strings.HasSuffix(l, "…") {
		t.Errorf("long line cut to %d runes: %.60q", n, l)
	}
}

func TestSection(t *testing.T) {
	text, heading, ok, _ := Section(combat, "stamina and fatigue")
	if !ok || heading != "Foundation › Stamina and Fatigue" || !strings.HasPrefix(text, "### Stamina and Fatigue\n") ||
		!strings.Contains(text, "stamina in code") || strings.Contains(text, "Open decisions") {
		t.Errorf("section under %q (found %v):\n%s", heading, ok, text)
	}
	// A section runs to the next heading at its level or above, so a ##
	// section holds its ### subsections.
	text, _, ok, _ = Section(combat, "Foundation")
	if !ok || !strings.Contains(text, "### Stamina and Fatigue") || strings.Contains(text, "## Open decisions") {
		t.Errorf("parent section:\n%s", text)
	}
	text, _, ok, _ = Section(combat, "Open decisions")
	if !ok || text != "## Open decisions\n\nNothing about it here.\n" {
		t.Errorf("last section %q", text)
	}
	if _, _, ok, all := Section(combat, "Not a heading"); ok || strings.Join(all, "|") != "Foundation|Stamina and Fatigue|Open decisions" {
		t.Errorf("fenced heading found, or headings %v", all)
	}
}
