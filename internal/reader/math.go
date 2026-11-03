package reader

import (
	"bytes"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// The content contract's math: $…$ inline and $$ blocks. The parsers keep
// the TeX away from emphasis and escape parsing; KaTeX renders it in the
// browser.

// KindMath is the kind of a Math node.
var KindMath = ast.NewNodeKind("Math")

// Math is TeX to typeset, inline or as a display block.
type Math struct {
	ast.BaseInline
	TeX     []byte
	Display bool
}

func (n *Math) Kind() ast.NodeKind { return KindMath }

func (n *Math) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"TeX": string(n.TeX)}, nil)
}

// KindMathBlock is the kind of a MathBlock node.
var KindMathBlock = ast.NewNodeKind("MathBlock")

// MathBlock is a $$ display block.
type MathBlock struct {
	ast.BaseBlock
	TeX  bytes.Buffer
	done bool
}

func (n *MathBlock) Kind() ast.NodeKind { return KindMathBlock }

func (n *MathBlock) IsRaw() bool { return true }

func (n *MathBlock) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"TeX": n.TeX.String()}, nil)
}

type mathExtension struct{}

func (mathExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithBlockParsers(util.Prioritized(mathBlockParser{}, 150)),
		parser.WithInlineParsers(util.Prioritized(mathInlineParser{}, 150)),
	)
}

type mathInlineParser struct{}

func (mathInlineParser) Trigger() []byte { return []byte{'$'} }

// Parse reads $tex$ or $$tex$$ on one line. A single-dollar span opens on a
// non-space and closes on a non-space not followed by a digit, so prices
// such as "$5 and $10" stay text, and it never spans a code span.
func (mathInlineParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, _ := block.PeekLine()
	n := 1
	if len(line) > 1 && line[1] == '$' {
		n = 2
	}
	if len(line) <= n || line[n] == ' ' || line[n] == '\t' || line[n] == '\n' {
		return nil
	}
	for i := n; i < len(line); i++ {
		switch {
		case line[i] == '\\':
			i++
		case line[i] == '\n' || line[i] == '`':
			// TeX has no backticks; a code span ends the attempt.
			return nil
		case line[i] == '$':
			if n == 2 && (i+1 >= len(line) || line[i+1] != '$') {
				continue
			}
			if n == 1 && (line[i-1] == ' ' || line[i-1] == '\t' || (i+1 < len(line) && line[i+1] >= '0' && line[i+1] <= '9')) {
				continue
			}
			block.Advance(i + n)
			return &Math{TeX: bytes.Clone(line[n:i]), Display: n == 2}
		}
	}
	return nil
}

type mathBlockParser struct{}

func (mathBlockParser) Trigger() []byte { return []byte{'$'} }

func (mathBlockParser) Open(_ ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, _ := reader.PeekLine()
	pos := pc.BlockOffset()
	if pos < 0 || !bytes.HasPrefix(line[pos:], []byte("$$")) {
		return nil, parser.NoChildren
	}
	rest := bytes.TrimSpace(line[pos+2:])
	node := &MathBlock{}
	if body, ok := bytes.CutSuffix(rest, []byte("$$")); ok {
		// $$tex$$ on one line.
		node.TeX.Write(bytes.TrimSpace(body))
		node.done = true
	} else {
		node.TeX.Write(rest)
	}
	reader.Advance(lineLen(line))
	return node, parser.NoChildren
}

func (mathBlockParser) Continue(node ast.Node, reader text.Reader, _ parser.Context) parser.State {
	n := node.(*MathBlock)
	if n.done {
		return parser.Close
	}
	line, _ := reader.PeekLine()
	trimmed := bytes.TrimSpace(line)
	body, closed := bytes.CutSuffix(trimmed, []byte("$$"))
	if n.TeX.Len() > 0 && (len(body) > 0 || !closed) {
		n.TeX.WriteByte('\n')
	}
	if closed {
		n.TeX.Write(bytes.TrimSpace(body))
	} else {
		n.TeX.Write(bytes.TrimRight(line, "\r\n"))
	}
	reader.Advance(lineLen(line))
	if closed {
		return parser.Close
	}
	return parser.Continue | parser.NoChildren
}

func (mathBlockParser) Close(ast.Node, text.Reader, parser.Context) {}

func (mathBlockParser) CanInterruptParagraph() bool { return true }

func (mathBlockParser) CanAcceptIndentedLine() bool { return false }

// lineLen is how far to advance over a line, leaving its newline to the
// block parser as goldmark's own parsers do.
func lineLen(line []byte) int {
	if n := len(line); n > 0 && line[n-1] == '\n' {
		return n - 1
	}
	return len(line)
}
