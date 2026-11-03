package reader

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// writeContent writes files under a new content root and opens it.
func writeContent(t *testing.T, files map[string]string) *os.Root {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return root
}

const clip = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// referenceContent is the formatting reference page with the files it uses.
func referenceContent(t *testing.T) *os.Root {
	t.Helper()
	ref, err := os.ReadFile("../facade/testdata/formatting-reference.md")
	if err != nil {
		t.Fatal(err)
	}
	return writeContent(t, map[string]string{
		"wiki/formatting-reference.md":                      strings.ReplaceAll(string(ref), "{{CLIP}}", clip),
		"wiki/formatting-reference.assets/test-card.png":    "png",
		"wiki/formatting-reference.assets/test-pattern.gif": "gif",
		"sources/agent-memory-survey.md":                    "---\ntype: source\ntitle: Survey\nsource_url: https://example.org/survey_(2026)\ncaptured: 2026-10-28\nsha256: " + clip + "\ncaptured_by_trail: run\n---\n\nbody\n",
	})
}

// TestFormattingReference renders every construct the content contract
// supports, from the reference page, and checks each one's HTML.
func TestFormattingReference(t *testing.T) {
	d, err := Render(referenceContent(t), "wiki/formatting-reference.md")
	if err != nil {
		t.Fatal(err)
	}
	body := string(d.Body)
	for _, want := range []string{
		`<h2 id="sections">Sections</h2>`,
		"<table>\n<thead>\n<tr>\n<th>Construct</th>",
		`<li><input checked="" disabled="" type="checkbox"> A finished item</li>`,
		`<sup id="fnref:1"><a href="#fn:1" class="footnote-ref" role="doc-noteref">1</a></sup>`,
		`<section class="footnotes" role="doc-endnotes"><h2 id="references">References</h2>`,
		`<a href="/sources/agent-memory-survey">The memory survey</a>`,
		`<a class="original" href="https://example.org/survey_(2026)" rel="noopener noreferrer">original&nbsp;↗</a>`,
		`<div class="callout callout-note" role="note"><p class="callout-title">Note</p>` + "\n<p>A note. TIP,",
		"<details>\n<summary>Method details</summary>\n",
		"<p>Long material most readers can skip.</p>\n</details>",
		`<pre class="mermaid">graph LR` + "\n" + `  S[&#34;Source (captured)&#34;] --&gt; T[Trail]`,
		`Inline <span class="math">E = mc^2</span>, and a block:`,
		`<div class="math display">\int_0^1 x^2 \, dx = \frac{1}{3}</div>`,
		`<p><img src="/wiki/formatting-reference.assets/test-card.png" alt="A colour test card, 2400 by 1350 pixels"></p>` + "\n<p><em>Figure 1.",
		`<img src="/wiki/formatting-reference.assets/test-pattern.gif" alt="An animated test pattern">`,
		`<img src="/wiki/formatting-reference.assets/test-card.png" alt="The test card at 600 pixels wide" width="600">`,
		`<video src="/media/` + clip + `.mp4" controls preload="metadata" title="A test pattern with a tone"></video>`,
		`<li><code>&lt;figure&gt;</code> and <code>&lt;figcaption&gt;</code>.`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "<h1") || strings.Contains(body, "<p>[!NOTE]") {
		t.Error("title heading or callout marker left in the body")
	}
	if !d.Mermaid || !d.Math || d.Citations != 1 {
		t.Errorf("mermaid %v math %v citations %d", d.Mermaid, d.Math, d.Citations)
	}
	if d.Title != "Formatting reference" || !strings.HasPrefix(d.Lede, "Every construct den-wiki supports") {
		t.Errorf("title %q lede %q", d.Title, d.Lede)
	}
	if n := len(d.Contents); n != 14 || d.Contents[0].ID != "sections" || d.Contents[n-1].ID != "references" {
		t.Errorf("contents %+v", d.Contents)
	}
}

func TestLinks(t *testing.T) {
	root := writeContent(t, map[string]string{
		"wiki/a.md":              "---\ntitle: A\n---\n\n[B](b.md#part), [gone](./gone.md), [trail](/trails/run.md), [web](https://x.org/), [up](../../etc.md), [file](a.assets/f.pdf), [home](index.md)\n",
		"wiki/b.md":              "b",
		"wiki/index.md":          "generated",
		"trails/run.md":          "r",
		"wiki/a.assets/f.pdf":    "%PDF",
		"wiki/my page/x y.md":    "x",
		"wiki/link-to-spaced.md": "[x](<my page/x y.md>)\n",
	})
	d, err := Render(root, "wiki/a.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<a href="/wiki/b#part">B</a>`,
		`<a href="/wiki/gone" class="new" title="This page does not exist yet">gone</a>`,
		`<a href="/trails/run">trail</a>`,
		`<a href="https://x.org/">web</a>`,
		`<a href="" class="new" title="This page does not exist yet">up</a>`,
		`<a href="/wiki/a.assets/f.pdf">file</a>`,
		`<a href="/">home</a>`,
	} {
		if !strings.Contains(string(d.Body), want) {
			t.Errorf("missing %q in %s", want, d.Body)
		}
	}
	d, err = Render(root, "wiki/link-to-spaced.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(d.Body), `href="/wiki/my%20page/x%20y"`) {
		t.Errorf("spaced link: %s", d.Body)
	}
}

func TestRawHTMLIsAllowlisted(t *testing.T) {
	root := writeContent(t, map[string]string{"wiki/a.md": `---
title: A
---

<script>alert(1)</script>

Text <span onclick="x()">inline</span> and <kbd>Ctrl</kbd>.

<img src="javascript:alert(1)" onerror="alert(2)" alt="x" width="100px" />

<details open onclick="x()">
<summary>S</summary>

Body

</details>

<!-- a comment -->
`})
	d, err := Render(root, "wiki/a.md")
	if err != nil {
		t.Fatal(err)
	}
	body := string(d.Body)
	for _, bad := range []string{"<script", "<span", "onerror", "a comment", `width="100px"`} {
		if strings.Contains(body, bad) {
			t.Errorf("%q survived: %s", bad, body)
		}
	}
	if handler := regexp.MustCompile(`<[a-z][^>]*\son[a-z]+=`); handler.MatchString(body) {
		t.Errorf("an event handler survived: %s", body)
	}
	for _, want := range []string{"&lt;script&gt;alert(1)&lt;/script&gt;", "<kbd>Ctrl</kbd>", "<details open>", `<img class="missing" src="/wiki/javascript:alert%281%29" alt="x">`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %s", want, body)
		}
	}
}

func TestMathLeavesPricesAlone(t *testing.T) {
	root := writeContent(t, map[string]string{"wiki/a.md": "It cost $5 and $10, or `$5`; $x_1 * y_2$ and $$a^2$$ inline.\n\n$$ b^2 $$\n\nAfter.\n"})
	d, err := Render(root, "wiki/a.md")
	if err != nil {
		t.Fatal(err)
	}
	body := string(d.Body)
	for _, want := range []string{
		"It cost $5 and $10, or <code>$5</code>;",
		`<span class="math">x_1 * y_2</span>`,
		`<span class="math display">a^2</span>`,
		`<div class="math display">b^2</div>`,
		"<p>After.</p>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %s", want, body)
		}
	}
}

func TestNotesWithoutCitations(t *testing.T) {
	root := writeContent(t, map[string]string{
		"wiki/a.md":    "---\ntitle: A\n---\n\n# A\n\n> [!WARNING]\n> Careful.\n\n> A plain quote.\n\nText.[^1] More.[^2]\n\n[^1]: Just a note.\n[^2]: [Two words](../sources/s.md) \"quote\"\n",
		"sources/s.md": "---\ntitle: S\n---\n\nno url\n",
	})
	d, err := Render(root, "wiki/a.md")
	if err != nil {
		t.Fatal(err)
	}
	body := string(d.Body)
	if d.Citations != 1 || !strings.Contains(body, `<h2 id="references">References</h2>`) || strings.Contains(body, `class="original"`) {
		t.Errorf("citations %d: %s", d.Citations, body)
	}
	if !strings.Contains(body, `<div class="callout callout-warning" role="note"><p class="callout-title">Warning</p>`) || !strings.Contains(body, "<blockquote>\n<p>A plain quote.</p>") {
		t.Errorf("callout: %s", body)
	}
	if d.Lede != "Text. More." {
		t.Errorf("lede %q", d.Lede)
	}
	root = writeContent(t, map[string]string{"wiki/b.md": "Text.[^n]\n\n[^n]: A note.\n"})
	if d, err = Render(root, "wiki/b.md"); err != nil || !strings.Contains(string(d.Body), `<h2 id="references">Notes</h2>`) || d.Title != "b" {
		t.Errorf("notes: %v %s", err, d.Body)
	}
}

func TestRenderRejectsBadPaths(t *testing.T) {
	root := writeContent(t, map[string]string{"wiki/a.md": "a"})
	for _, rel := range []string{"../a.md", "wiki/../wiki/a.md", "wiki/missing.md", "wiki/a.assets/x.png", ""} {
		if _, err := Render(root, rel); err == nil {
			t.Errorf("%q rendered", rel)
		}
	}
}
