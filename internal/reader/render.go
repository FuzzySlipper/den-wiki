// Package reader is den-wiki's read-only reader: the wiki rendered to HTML on
// the server, with a topic tree, contents, references and backlinks. OK stays
// the editor; the reader never writes.
package reader

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/FuzzySlipper/den-wiki/internal/content"
	"github.com/FuzzySlipper/den-wiki/internal/media"
)

// ErrNotFound is returned for a file that does not exist.
var ErrNotFound = errors.New("not found")

// Heading is one entry of a page's contents.
type Heading struct {
	Level int
	ID    string
	Text  string
}

// Doc is a rendered page, source or trail.
type Doc struct {
	// Path is the content-root-relative file path.
	Path   string
	Fields content.Frontmatter
	Title  string
	// Body is the rendered Markdown, without a leading title heading.
	Body template.HTML
	// Contents lists the ## and ### headings.
	Contents []Heading
	// Lede is the first paragraph as plain text, for listings.
	Lede string
	// Citations counts the footnotes that cite a source.
	Citations int
	// Mermaid and Math say which scripts the page needs.
	Mermaid, Math bool
}

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM, extension.Footnote, mathExtension{}),
	goldmark.WithParserOptions(
		parser.WithAutoHeadingID(),
		parser.WithASTTransformers(util.Prioritized(transformer{}, 100)),
	),
	goldmark.WithRendererOptions(
		html.WithUnsafe(), // raw HTML goes through sanitize in nodeRenderer
		renderer.WithNodeRenderers(util.Prioritized(nodeRenderer{}, 100)),
	),
)

var docKey = parser.NewContextKey()

// Render reads and renders the Markdown file at rel.
func Render(root *os.Root, rel string) (*Doc, error) {
	if !content.Clean(rel) || !strings.HasSuffix(rel, ".md") {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, rel)
	}
	src, err := root.ReadFile(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, rel)
	}
	if err != nil {
		return nil, err
	}
	fields, body, err := content.Split(src)
	if err != nil {
		// A broken frontmatter block is shown as part of the text.
		fields, body = content.Frontmatter{}, src
	}
	d := &Doc{Path: rel, Fields: fields, Title: fields.Str("title")}
	if d.Title == "" {
		d.Title = fields.Str("question")
	}
	if d.Title == "" {
		d.Title = strings.TrimSuffix(path.Base(rel), ".md")
	}
	st := &state{doc: d, root: root}
	pc := parser.NewContext()
	pc.Set(docKey, st)
	tree := md.Parser().Parse(text.NewReader(body), parser.WithContext(pc))
	var buf bytes.Buffer
	if err := md.Renderer().Render(&buf, body, tree); err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	d.Body = template.HTML(buf.String())
	return d, nil
}

// state is one render's page and content root, reached by the transformer.
type state struct {
	doc  *Doc
	root *os.Root
}

func (s *state) exists(rel string) bool {
	_, err := s.root.Stat(rel)
	return err == nil
}

// Href maps a link target in the page at rel to a reader URL. Content paths
// keep their shape: wiki/agent-memory.md is /wiki/agent-memory and an asset
// is served at its own path. missing reports a content target that does not
// exist; an empty href is a target outside the content.
func (s *state) href(target string) (href string, missing bool) {
	if content.IsExternal(target) || media.RefRe.MatchString(target) && strings.HasPrefix(target, media.URLPrefix) {
		return target, false
	}
	resolved, ok := content.Resolve(s.doc.Path, target)
	if !ok {
		return "", true
	}
	frag := ""
	if i := strings.IndexByte(target, '#'); i >= 0 {
		frag = target[i:]
	}
	return URL(resolved) + frag, !s.exists(resolved)
}

// URL is the reader URL of a content path.
func URL(rel string) string {
	if rel == "index.md" || path.Base(rel) == "index.md" && path.Dir(rel) == "wiki" {
		return "/"
	}
	rel = strings.TrimSuffix(rel, ".md")
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return "/" + strings.Join(parts, "/")
}

var calloutRe = regexp.MustCompile(`^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*$`)

type transformer struct{}

func (transformer) Transform(tree *ast.Document, reader text.Reader, pc parser.Context) {
	st := pc.Get(docKey).(*state)
	src := reader.Source()
	d := st.doc
	if h, ok := firstBlock(tree).(*ast.Heading); ok && h.Level == 1 && strings.EqualFold(strings.TrimSpace(plain(h, src)), d.Title) {
		tree.RemoveChild(tree, h)
	}
	var quotes []*ast.Blockquote
	var list *east.FootnoteList
	cited := map[*east.Footnote]string{}
	_ = ast.Walk(tree, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Heading:
			id, _ := n.AttributeString("id")
			idb, _ := id.([]byte)
			if (n.Level == 2 || n.Level == 3) && !inFootnote(n) {
				d.Contents = append(d.Contents, Heading{Level: n.Level, ID: string(idb), Text: plain(n, src)})
			}
		case *ast.Paragraph:
			if d.Lede == "" && n.Parent() == tree {
				d.Lede = lede(plain(n, src))
			}
		case *ast.Blockquote:
			quotes = append(quotes, n)
		case *ast.Link:
			href, missing := st.href(string(n.Destination))
			n.Destination = []byte(href)
			if missing {
				n.SetAttributeString("class", []byte("new"))
				n.SetAttributeString("title", []byte("This page does not exist yet"))
			}
		case *ast.Image:
			href, _ := st.href(string(n.Destination))
			n.Destination = []byte(href)
		case *ast.FencedCodeBlock:
			if string(n.Language(src)) == "mermaid" {
				d.Mermaid = true
			}
		case *Math, *MathBlock:
			d.Math = true
		case *ast.RawHTML:
			var b strings.Builder
			for i := 0; i < n.Segments.Len(); i++ {
				b.Write(linesAt(n.Segments, i, src))
			}
			n.SetAttributeString(safeAttr, []byte(sanitize(b.String(), st.href)))
		case *ast.HTMLBlock:
			var b strings.Builder
			for i := 0; i < n.Lines().Len(); i++ {
				b.Write(linesAt(n.Lines(), i, src))
			}
			if n.HasClosure() {
				b.Write(n.ClosureLine.Value(src))
			}
			n.SetAttributeString(safeAttr, []byte(sanitize(b.String(), st.href)))
		case *east.FootnoteList:
			list = n
		case *east.Footnote:
			if u := st.citation(n); u != "" {
				d.Citations++
				cited[n] = u
			}
		}
		return ast.WalkContinue, nil
	})
	for _, q := range quotes {
		callout(q, src)
	}
	for fn, u := range cited {
		if u != "-" {
			addOriginal(fn, u)
		}
	}
	if list != nil {
		heading := "Notes"
		if d.Citations > 0 {
			heading = "References"
		}
		list.SetAttributeString("data-heading", []byte(heading))
		d.Contents = append(d.Contents, Heading{Level: 2, ID: "references", Text: heading})
	}
}

// addOriginal puts a link to the source's original URL at the end of a
// citation, before goldmark's back-links.
func addOriginal(fn *east.Footnote, u string) {
	var container ast.Node = fn
	if p, ok := fn.LastChild().(*ast.Paragraph); ok {
		container = p
	}
	o := &Original{URL: u}
	for c := container.FirstChild(); c != nil; c = c.NextSibling() {
		if _, ok := c.(*east.FootnoteBacklink); ok {
			container.InsertBefore(container, c, o)
			return
		}
	}
	container.AppendChild(container, o)
}

// citation returns the source_url of the source a footnote cites, "-" for a
// cited source with no URL, or "" when the footnote cites nothing.
func (s *state) citation(fn *east.Footnote) string {
	out := ""
	_ = ast.Walk(fn, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		l, ok := n.(*ast.Link)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}
		target := string(l.Destination)
		if content.IsExternal(target) {
			return ast.WalkContinue, nil
		}
		rel, ok := content.Resolve(s.doc.Path, target)
		if !ok || !strings.HasPrefix(rel, "sources/") || !strings.HasSuffix(rel, ".md") {
			return ast.WalkContinue, nil
		}
		out = content.SourceURL(s.root, rel)
		if out == "" {
			out = "-"
		}
		return ast.WalkStop, nil
	})
	return out
}

func firstBlock(tree ast.Node) ast.Node {
	for c := tree.FirstChild(); c != nil; c = c.NextSibling() {
		if _, ok := c.(*ast.HTMLBlock); !ok {
			return c
		}
	}
	return nil
}

func inFootnote(n ast.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if _, ok := p.(*east.Footnote); ok {
			return true
		}
	}
	return false
}

// plain returns a node's text with the markup taken out.
func plain(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch c := c.(type) {
		case *ast.Text:
			b.Write(c.Segment.Value(src))
			if c.SoftLineBreak() || c.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(c.Value)
		case *ast.CodeSpan:
			for t := c.FirstChild(); t != nil; t = t.NextSibling() {
				if s, ok := t.(*ast.Text); ok {
					b.Write(s.Segment.Value(src))
				}
			}
			return ast.WalkSkipChildren, nil
		case *Math:
			b.Write(c.TeX)
		case *east.FootnoteLink:
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(b.String())
}

func lede(s string) string {
	const max = 240
	if len(s) <= max {
		return s
	}
	cut := strings.LastIndexByte(s[:max], ' ')
	if cut < max/2 {
		cut = max
	}
	return s[:cut] + "…"
}

// KindCallout is the kind of a Callout node.
var KindCallout = ast.NewNodeKind("Callout")

// Callout is a GitHub-style alert, > [!NOTE] and its kin.
type Callout struct {
	ast.BaseBlock
	Alert string
}

func (n *Callout) Kind() ast.NodeKind { return KindCallout }

func (n *Callout) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Alert": n.Alert}, nil)
}

// callout turns a blockquote whose first line is [!TYPE] into a Callout.
func callout(q *ast.Blockquote, src []byte) {
	p, ok := q.FirstChild().(*ast.Paragraph)
	if !ok || p.Lines().Len() == 0 {
		return
	}
	first := p.Lines().At(0)
	m := calloutRe.FindSubmatch(bytes.TrimSpace(first.Value(src)))
	if m == nil {
		return
	}
	// Drop the inline nodes of the marker line.
	for c := p.FirstChild(); c != nil; {
		next := c.NextSibling()
		t, ok := c.(*ast.Text)
		if !ok || t.Segment.Start >= first.Stop {
			break
		}
		p.RemoveChild(p, c)
		c = next
	}
	if p.FirstChild() == nil {
		q.RemoveChild(q, p)
	}
	c := &Callout{Alert: strings.ToLower(string(m[1]))}
	for ch := q.FirstChild(); ch != nil; {
		next := ch.NextSibling()
		c.AppendChild(c, ch)
		ch = next
	}
	q.Parent().ReplaceChild(q.Parent(), q, c)
}
