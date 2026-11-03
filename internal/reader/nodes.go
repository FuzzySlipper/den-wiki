package reader

import (
	"html"
	"strings"

	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// KindOriginal is the kind of an Original node.
var KindOriginal = ast.NewNodeKind("Original")

// Original links a citation to the source's original URL.
type Original struct {
	ast.BaseInline
	URL string
}

func (n *Original) Kind() ast.NodeKind { return KindOriginal }

func (n *Original) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"URL": n.URL}, nil)
}

// safeAttr holds a raw HTML node's sanitized form, set by the transformer.
const safeAttr = "data-reader-safe"

// nodeRenderer renders the reader's own nodes and replaces goldmark's
// rendering of raw HTML, fenced code and footnotes.
type nodeRenderer struct{}

func (nodeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(KindMath, renderMath)
	reg.Register(KindMathBlock, renderMathBlock)
	reg.Register(KindCallout, renderCallout)
	reg.Register(KindOriginal, renderOriginal)
	reg.Register(ast.KindRawHTML, renderSafe)
	reg.Register(ast.KindHTMLBlock, renderSafe)
	reg.Register(ast.KindFencedCodeBlock, renderFence)
	reg.Register(east.KindFootnoteList, renderFootnoteList)
}

func renderMath(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		n := node.(*Math)
		class := "math"
		if n.Display {
			class = "math display"
		}
		_, _ = w.WriteString(`<span class="` + class + `">` + html.EscapeString(string(n.TeX)) + `</span>`)
	}
	return ast.WalkSkipChildren, nil
}

func renderMathBlock(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		n := node.(*MathBlock)
		_, _ = w.WriteString(`<div class="math display">` + html.EscapeString(n.TeX.String()) + "</div>\n")
	}
	return ast.WalkSkipChildren, nil
}

var calloutTitles = map[string]string{"note": "Note", "tip": "Tip", "important": "Important", "warning": "Warning", "caution": "Caution"}

func renderCallout(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*Callout)
	if entering {
		_, _ = w.WriteString(`<div class="callout callout-` + n.Alert + `" role="note"><p class="callout-title">` + calloutTitles[n.Alert] + "</p>\n")
	} else {
		_, _ = w.WriteString("</div>\n")
	}
	return ast.WalkContinue, nil
}

func renderOriginal(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		u := node.(*Original).URL
		_, _ = w.WriteString(` <a class="original" href="` + html.EscapeString(u) + `" rel="noopener noreferrer">original&nbsp;↗</a>`)
	}
	return ast.WalkSkipChildren, nil
}

func renderSafe(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		if v, ok := node.AttributeString(safeAttr); ok {
			b, _ := v.([]byte)
			_, _ = w.Write(b)
		}
	}
	return ast.WalkSkipChildren, nil
}

func renderFence(w util.BufWriter, src []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkSkipChildren, nil
	}
	n := node.(*ast.FencedCodeBlock)
	lang := string(n.Language(src))
	switch lang {
	case "mermaid":
		_, _ = w.WriteString(`<pre class="mermaid">`)
	case "":
		_, _ = w.WriteString(`<pre><code>`)
	default:
		_, _ = w.WriteString(`<pre><code class="language-` + html.EscapeString(lang) + `">`)
	}
	var body strings.Builder
	for i := 0; i < n.Lines().Len(); i++ {
		body.Write(linesAt(n.Lines(), i, src))
	}
	_, _ = w.WriteString(html.EscapeString(body.String()))
	if lang == "mermaid" {
		_, _ = w.WriteString("</pre>\n")
	} else {
		_, _ = w.WriteString("</code></pre>\n")
	}
	return ast.WalkSkipChildren, nil
}

// renderFootnoteList gathers the footnotes under References (or Notes, when
// none cites a source); goldmark's renderer draws each footnote.
func renderFootnoteList(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		heading := "Notes"
		if v, ok := node.AttributeString("data-heading"); ok {
			b, _ := v.([]byte)
			heading = string(b)
		}
		_, _ = w.WriteString(`<section class="footnotes" role="doc-endnotes"><h2 id="references">` + heading + "</h2>\n<ol>\n")
	} else {
		_, _ = w.WriteString("</ol>\n</section>\n")
	}
	return ast.WalkContinue, nil
}

// linesAt returns the i-th segment's bytes.
func linesAt(segs *text.Segments, i int, src []byte) []byte {
	s := segs.At(i)
	return s.Value(src)
}
