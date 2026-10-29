package lint

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// document is a Markdown file split into its frontmatter and body.
type document struct {
	hasFrontmatter bool
	// fields is the frontmatter as JSON-compatible values. Timestamps stay
	// strings so the schemas see exactly what was written.
	fields map[string]any
	body   []byte
	// bodyLine is the 1-based line number of the first body line.
	bodyLine int
}

var errNotMapping = errors.New("frontmatter is not a YAML mapping")

// parseDocument splits src at the frontmatter delimiters. A file has
// frontmatter when its first line is exactly "---"; the block ends at the next
// line that is exactly "---" or "...". The body is every byte after that
// closing line, which is what a source's sha256 covers.
func parseDocument(src []byte) (document, error) {
	first, rest, ok := cutLine(src)
	if !ok || first != "---" {
		return document{body: src, bodyLine: 1}, nil
	}
	consumed := len(src) - len(rest)
	line := 2
	for len(rest) > 0 {
		l, next, _ := cutLine(rest)
		if l == "---" || l == "..." {
			block := src[consumed : len(src)-len(rest)]
			fields, err := decodeFrontmatter(block)
			if err != nil {
				return document{}, err
			}
			return document{hasFrontmatter: true, fields: fields, body: next, bodyLine: line + 1}, nil
		}
		rest = next
		line++
	}
	return document{}, errors.New("frontmatter block is not closed by a --- line")
}

// cutLine returns the first line of b without its terminator, the remainder,
// and whether b was non-empty.
func cutLine(b []byte) (string, []byte, bool) {
	if len(b) == 0 {
		return "", nil, false
	}
	i := bytes.IndexByte(b, '\n')
	if i < 0 {
		return strings.TrimSuffix(string(b), "\r"), nil, true
	}
	return strings.TrimSuffix(string(b[:i]), "\r"), b[i+1:], true
}

func decodeFrontmatter(block []byte) (map[string]any, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(block, &root); err != nil {
		return nil, err
	}
	if root.Kind == 0 {
		return map[string]any{}, nil
	}
	v, err := nodeValue(&root)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !ok {
		if v == nil {
			return map[string]any{}, nil
		}
		return nil, errNotMapping
	}
	return m, nil
}

func nodeValue(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return nodeValue(n.Content[0])
	case yaml.AliasNode:
		return nodeValue(n.Alias)
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			v, err := nodeValue(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			m[n.Content[i].Value] = v
		}
		return m, nil
	case yaml.SequenceNode:
		s := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := nodeValue(c)
			if err != nil {
				return nil, err
			}
			s = append(s, v)
		}
		return s, nil
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!null":
			return nil, nil
		case "!!bool":
			var b bool
			if err := n.Decode(&b); err != nil {
				return nil, err
			}
			return b, nil
		case "!!int":
			i, err := strconv.ParseInt(n.Value, 0, 64)
			if err != nil {
				return n.Value, nil
			}
			return json.Number(strconv.FormatInt(i, 10)), nil
		case "!!float":
			var f float64
			if err := n.Decode(&f); err != nil {
				return nil, err
			}
			return json.Number(strconv.FormatFloat(f, 'g', -1, 64)), nil
		default:
			return n.Value, nil
		}
	}
	return nil, fmt.Errorf("unsupported YAML node kind %d", n.Kind)
}

func bodySHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func stringField(fields map[string]any, key string) (string, bool) {
	s, ok := fields[key].(string)
	return s, ok
}

// stringList returns the string items of a list field, skipping anything else;
// the schema reports wrong item types.
func stringList(fields map[string]any, key string) []string {
	items, _ := fields[key].([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
