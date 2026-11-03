package reader

import (
	"html"
	"regexp"
	"strings"

	"github.com/FuzzySlipper/den-wiki/internal/content"
)

// Raw HTML in a page passes through only for the elements the content
// contract allows, with safe attributes. Anything else is shown as text, so
// a page cannot run script in the reader.

var allowedTags = map[string]map[string]bool{
	"img":     {"src": true, "alt": true, "width": true, "height": true, "title": true},
	"video":   {"src": true, "controls": true, "preload": true, "title": true, "poster": true, "width": true, "height": true, "loop": true, "muted": true},
	"details": {"open": true},
	"summary": {},
	"br":      {},
	"sub":     {},
	"sup":     {},
	"kbd":     {},
}

var voidTags = map[string]bool{"img": true, "br": true}

var boolAttrs = map[string]bool{"controls": true, "loop": true, "muted": true, "open": true}

var (
	tagRe  = regexp.MustCompile(`(?s)<!--.*?-->|<(/?)([a-zA-Z][a-zA-Z0-9-]*)((?:\s+[^\s/>=]+(?:\s*=\s*(?:"[^"]*"|'[^']*'|\{[^}]*\}|[^\s>]+))?)*)\s*(/?)>`)
	attrRe = regexp.MustCompile(`([^\s/>=]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|\{([^}]*)\}|([^\s>]+)))?`)
	numRe  = regexp.MustCompile(`^[0-9]+%?$`)
)

// textEscaper escapes the angle brackets of text in an HTML fragment, leaving
// entities as written.
var textEscaper = strings.NewReplacer("<", "&lt;", ">", "&gt;")

// sanitize rewrites an HTML fragment from a page: allowed elements are
// rebuilt with their allowed attributes, src and poster go through link,
// comments are dropped, and everything else is escaped.
func sanitize(frag string, link func(string) (string, bool)) string {
	var b strings.Builder
	last := 0
	for _, m := range tagRe.FindAllStringSubmatchIndex(frag, -1) {
		b.WriteString(textEscaper.Replace(frag[last:m[0]]))
		last = m[1]
		if m[4] < 0 {
			continue // a comment
		}
		closing, name := frag[m[2]:m[3]] == "/", strings.ToLower(frag[m[4]:m[5]])
		attrs, ok := allowedTags[name]
		if !ok {
			b.WriteString(textEscaper.Replace(frag[m[0]:m[1]]))
			continue
		}
		if closing {
			if !voidTags[name] {
				b.WriteString("</" + name + ">")
			}
			continue
		}
		b.WriteString("<" + name)
		for _, a := range attrRe.FindAllStringSubmatch(content.FixJSXAttrs(frag[m[6]:m[7]]), -1) {
			key := strings.ToLower(a[1])
			if !attrs[key] {
				continue
			}
			val := html.UnescapeString(a[2] + a[3] + a[4] + a[5])
			switch {
			case boolAttrs[key]:
				b.WriteString(" " + key)
				continue
			case key == "src" || key == "poster":
				href, missing := link(val)
				if href == "" || missing {
					b.WriteString(` class="missing"`)
				}
				if href == "" {
					continue
				}
				val = href
			case key == "width" || key == "height":
				if !numRe.MatchString(val) {
					continue
				}
			}
			b.WriteString(" " + key + `="` + html.EscapeString(val) + `"`)
		}
		b.WriteString(">")
		if frag[m[8]:m[9]] == "/" && !voidTags[name] {
			// JSX-style <video … />: HTML has no self-closing video.
			b.WriteString("</" + name + ">")
		}
	}
	b.WriteString(textEscaper.Replace(frag[last:]))
	return b.String()
}
