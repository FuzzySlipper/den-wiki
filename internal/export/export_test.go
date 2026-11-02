package export

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/FuzzySlipper/den-wiki/internal/media"
)

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const page = `---
type: concept
title: Agent memory
updated: 2026-11-02
---

# Agent memory

Agents with memory answer better.[^1] See [the tools page](tools.md) and [a trail](../trails/run.md#notes).
Read [the paper](https://example.org/paper) or [an uncaptured note](../sources/no-url.md).

> [!WARNING]
> Memory goes stale.

![The loop](./agent-memory.assets/loop.png "Loop")

![Figure 2](../sources/survey.assets/loop.png)

<img src="./agent-memory.assets/loop.png" alt="Small loop" width={600} />

<video src="/media/` + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + `.mp4" controls title="A demo clip" />

Use ` + "`![chart](chart.png)`" + ` never, and [a PDF](./agent-memory.assets/paper.pdf).

~~~markdown
[^1]: [The survey](../sources/survey.md) "quote"
` + "```mermaid" + `
graph LR
` + "```" + `
~~~

` + "```mermaid" + `
graph LR
  A --> B
` + "```" + `

$$
x^2
$$

[^1]: [The memory survey](../sources/survey.md) "Agents that keep an external memory answer better"
`

func fixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "wiki/agent-memory.md", page)
	write(t, dir, "wiki/agent-memory.assets/loop.png", "png-a")
	write(t, dir, "wiki/agent-memory.assets/paper.pdf", "pdf")
	write(t, dir, "sources/survey.assets/loop.png", "png-b")
	write(t, dir, "sources/survey.md", "---\ntitle: Survey\nsource_url: https://example.org/survey_(2026)\n---\nbody\n")
	write(t, dir, "sources/no-url.md", "---\ntitle: Note\n---\nbody\n")
	write(t, dir, "wiki/tools.md", "---\ntitle: Tools\n---\n")
	return dir
}

func TestExportOnePageForOutsideReaders(t *testing.T) {
	p, err := Export(fixture(t), "wiki/agent-memory.md", "https://blog.example/assets/wiki/agent-memory/")
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Agent memory" || p.Updated != "2026-11-02" {
		t.Errorf("title %q updated %q", p.Title, p.Updated)
	}
	md := p.Markdown
	const base = "https://blog.example/assets/wiki/agent-memory/"
	for _, want := range []string{
		"Agents with memory answer better.[^1] See the tools page and a trail.",
		"[the paper](https://example.org/paper) or an uncaptured note.",
		"> **Warning:**\n> Memory goes stale.",
		"![The loop](" + base + "loop.png \"Loop\")",
		"![Figure 2](" + base + "loop-2.png)",
		`<img src="` + base + `loop.png" alt="Small loop" width="600" />`,
		"*Video not included: A demo clip.*",
		"Use `![chart](chart.png)` never, and [a PDF](" + base + "paper.pdf).",
		"[^1]: [The memory survey](https://example.org/survey_%282026%29) \"Agents that keep",
		// Fenced examples are left exactly as written.
		"~~~markdown\n[^1]: [The survey](../sources/survey.md) \"quote\"\n```mermaid\ngraph LR\n```\n~~~",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	for _, not := range []string{"---\ntype:", "# Agent memory", "tools.md", "trails/", "/media/", "{600}"} {
		if strings.Contains(md, not) {
			t.Errorf("markdown still has %q:\n%s", not, md)
		}
	}
	var names []string
	for _, a := range p.Assets {
		names = append(names, a.Name+"="+a.Path)
	}
	if want := []string{"loop.png=wiki/agent-memory.assets/loop.png", "loop-2.png=sources/survey.assets/loop.png", "paper.pdf=wiki/agent-memory.assets/paper.pdf"}; !slices.Equal(names, want) {
		t.Errorf("assets %v, want %v", names, want)
	}
	if a := p.Assets[0]; a.SHA256 != media.Hash([]byte("png-a")) || a.Size != 5 || a.ContentType != "image/png" {
		t.Errorf("asset %+v", a)
	}
	warnings := strings.Join(p.Warnings, "\n")
	for _, want := range []string{"sources/no-url.md has no source_url", "A demo clip", "Mermaid", "math"} {
		if !strings.Contains(warnings, want) {
			t.Errorf("warnings lack %q:\n%s", want, warnings)
		}
	}
}

func TestExportOnlyWikiPages(t *testing.T) {
	dir := fixture(t)
	for _, rel := range []string{"sources/survey.md", "wiki/../sources/survey.md", "/wiki/agent-memory.md", "wiki/agent-memory.assets/loop.png"} {
		if _, err := Export(dir, rel, ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err %v, want ErrInvalid", rel, err)
		}
	}
	if _, err := Export(dir, "wiki/missing.md", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing page: err %v, want ErrNotFound", err)
	}
}

func TestReadAssetOnlyListedFiles(t *testing.T) {
	dir := fixture(t)
	a, b, err := ReadAsset(dir, "wiki/agent-memory.md", "loop-2.png")
	if err != nil || string(b) != "png-b" || a.Path != "sources/survey.assets/loop.png" {
		t.Fatalf("asset %+v %q %v", a, b, err)
	}
	if _, _, err := ReadAsset(dir, "wiki/agent-memory.md", "../tools.md"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unlisted asset: err %v, want ErrNotFound", err)
	}
}

func TestExportKeepsAnUnrelatedFirstHeading(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "wiki/x.md", "---\ntitle: X\n---\n\n# Background\n\nText.\n")
	p, err := Export(dir, "wiki/x.md", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p.Markdown, "# Background\n") {
		t.Errorf("markdown %q", p.Markdown)
	}
}

// The formatting reference holds every supported construct, with each
// example's source in a fenced block; the export keeps those blocks exact.
func TestExportFormattingReference(t *testing.T) {
	src, err := os.ReadFile("../facade/testdata/formatting-reference.md")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write(t, dir, "wiki/formatting-reference.md", string(src))
	write(t, dir, "wiki/formatting-reference.assets/test-card.png", "card")
	write(t, dir, "wiki/formatting-reference.assets/test-pattern.gif", "gif")
	write(t, dir, "sources/agent-memory-survey.md", "---\nsource_url: https://example.org/survey\n---\n")
	p, err := Export(dir, "wiki/formatting-reference.md", "/a/")
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range markdownFences(string(src)) {
		if !strings.Contains(p.Markdown, block) {
			t.Errorf("fenced block changed:\n%s", block)
		}
	}
	if len(p.Assets) != 2 {
		t.Errorf("assets %+v", p.Assets)
	}
	if !strings.Contains(p.Markdown, "`![chart](chart.png)`") {
		t.Error("code span rewritten")
	}
}

func markdownFences(s string) []string {
	var out []string
	parts := strings.Split(s, "~~~markdown\n")
	for _, part := range parts[1:] {
		if i := strings.Index(part, "\n~~~"); i >= 0 {
			out = append(out, "~~~markdown\n"+part[:i+4])
		}
	}
	return out
}
