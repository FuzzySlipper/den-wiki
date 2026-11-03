// Package content holds what den-wiki's renderers share about content files:
// frontmatter as written, link resolution between content paths, and a
// source's original URL. The export (one page for outside readers) and the
// reader (the whole wiki on the LAN) both build on it.
package content

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Frontmatter is a file's frontmatter, keyed by field.
type Frontmatter map[string]*yaml.Node

// Str returns a scalar field as written (a date stays "2026-10-29"), or "".
func (f Frontmatter) Str(key string) string {
	if n := f[key]; n != nil && n.Kind == yaml.ScalarNode && n.Tag != "!!null" {
		return n.Value
	}
	return ""
}

// List returns a list field's scalar items, skipping anything else.
func (f Frontmatter) List(key string) []string {
	n := f[key]
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	var out []string
	for _, it := range n.Content {
		if it.Kind == yaml.ScalarNode && it.Value != "" {
			out = append(out, it.Value)
		}
	}
	return out
}

// Split returns a file's frontmatter and its body: every byte after the line
// that closes the frontmatter. A file with no frontmatter block returns an
// empty Frontmatter and the whole file.
func Split(src []byte) (Frontmatter, []byte, error) {
	fields := Frontmatter{}
	rest, ok := bytes.CutPrefix(src, []byte("---\n"))
	if !ok {
		return fields, src, nil
	}
	end := 0
	for {
		i := bytes.IndexByte(rest[end:], '\n')
		line := rest[end:]
		if i >= 0 {
			line = rest[end : end+i]
		}
		if l := string(bytes.TrimSuffix(line, []byte("\r"))); l == "---" || l == "..." {
			var nodes map[string]yaml.Node
			if err := yaml.Unmarshal(rest[:end], &nodes); err != nil {
				return nil, nil, fmt.Errorf("frontmatter: %w", err)
			}
			for k := range nodes {
				n := nodes[k]
				fields[k] = &n
			}
			if i < 0 {
				return fields, nil, nil
			}
			return fields, rest[end+i+1:], nil
		}
		if i < 0 {
			return nil, nil, errors.New("frontmatter block is not closed")
		}
		end += i + 1
	}
}

// IsExternal reports whether a link target leaves the content: a URL, an
// in-page anchor or a mail address.
func IsExternal(target string) bool {
	return strings.Contains(target, "://") || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "mailto:")
}

// Resolve turns a link target into a content-root-relative path, dropping
// any query or fragment. Targets outside the content root report false.
func Resolve(fromRel, target string) (string, bool) {
	if i := strings.IndexAny(target, "#?"); i >= 0 {
		target = target[:i]
	}
	if target == "" {
		return "", false
	}
	var p string
	if strings.HasPrefix(target, "/") {
		p = path.Clean(strings.TrimPrefix(target, "/"))
	} else {
		p = path.Clean(path.Join(path.Dir(fromRel), target))
	}
	if p == ".." || strings.HasPrefix(p, "../") || p == "." {
		return "", false
	}
	return p, true
}

// SourceURL returns the http(s) source_url of the source at rel, or "".
func SourceURL(root *os.Root, rel string) string {
	src, err := root.ReadFile(rel)
	if err != nil {
		return ""
	}
	fields, _, err := Split(src)
	if err != nil {
		return ""
	}
	u := fields.Str("source_url")
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return ""
	}
	return u
}

// jsxNum matches the JSX numeric attributes OK's editor writes on images,
// width={600}, which HTML does not understand.
var jsxNum = regexp.MustCompile(`\b(width|height)=\{(\d+)\}`)

// FixJSXAttrs rewrites width={N} and height={N} to HTML attributes.
func FixJSXAttrs(el string) string { return jsxNum.ReplaceAllString(el, `$1="$2"`) }

// IsPage reports whether rel is a clean content-relative path to a wiki page.
func IsPage(rel string) bool {
	return Clean(rel) && strings.HasPrefix(rel, "wiki/") && strings.HasSuffix(rel, ".md")
}

// Clean reports whether rel is a clean, relative path inside the content.
func Clean(rel string) bool {
	return rel != "" && path.Clean(rel) == rel && !path.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, "../")
}
