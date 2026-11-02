// Package export renders one wiki page for readers outside the LAN, for
// sharing it on its own (doc-publish's blog). It exports the page alone,
// never the pages it links to: links to other content become plain text,
// citations point at the original source, and the files the page shows are
// listed so the publisher can copy them beside the post.
package export

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/FuzzySlipper/den-wiki/internal/media"
)

// Page is an exported page.
type Page struct {
	// Path is the content-root-relative page path.
	Path  string `json:"path"`
	Title string `json:"title"`
	// Updated is the page's updated frontmatter value, as written.
	Updated string `json:"updated,omitempty"`
	// Markdown is the body for outside readers: no frontmatter, no leading
	// title heading, and only links that work off the LAN.
	Markdown string `json:"markdown"`
	// Assets are the content files the Markdown shows or links, which the
	// publisher must copy to the asset base.
	Assets   []Asset  `json:"assets"`
	Warnings []string `json:"warnings"`
}

// Asset is one content file an exported page uses.
type Asset struct {
	// Name is the file's name under the asset base, unique within the page.
	Name string `json:"name"`
	// Path is the content-root-relative path it is read from.
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

// ErrNotFound is returned for a page or asset that does not exist.
var ErrNotFound = errors.New("not found")

// ErrInvalid is returned for a path that is not an exportable page.
var ErrInvalid = errors.New("invalid page")

// Export renders the wiki page at rel (content-root-relative, under wiki/).
// Asset links are rewritten to assetBase+name; an empty assetBase leaves the
// bare names.
func Export(contentDir, rel, assetBase string) (*Page, error) {
	if err := checkPage(rel); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(contentDir)
	if err != nil {
		return nil, fmt.Errorf("opening content dir: %w", err)
	}
	defer root.Close()
	src, err := root.ReadFile(rel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, rel)
	}
	if err != nil {
		return nil, err
	}
	fields, body, err := splitFrontmatter(src)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	e := &exporter{root: root, rel: rel, base: assetBase, byPath: map[string]string{}, names: map[string]bool{}}
	p := &Page{Path: rel, Title: fields["title"], Updated: fields["updated"]}
	if p.Title == "" {
		p.Title = strings.TrimSuffix(path.Base(rel), ".md")
	}
	p.Markdown = e.body(string(body), p.Title)
	p.Assets, p.Warnings = e.assets, e.warnings
	if p.Assets == nil {
		p.Assets = []Asset{}
	}
	if p.Warnings == nil {
		p.Warnings = []string{}
	}
	return p, nil
}

// ReadAsset returns the bytes of the asset the page at rel lists as name.
func ReadAsset(contentDir, rel, name string) (Asset, []byte, error) {
	p, err := Export(contentDir, rel, "")
	if err != nil {
		return Asset{}, nil, err
	}
	for _, a := range p.Assets {
		if a.Name != name {
			continue
		}
		root, err := os.OpenRoot(contentDir)
		if err != nil {
			return Asset{}, nil, err
		}
		defer root.Close()
		b, err := root.ReadFile(a.Path)
		if err != nil {
			return Asset{}, nil, err
		}
		return a, b, nil
	}
	return Asset{}, nil, fmt.Errorf("%w: %s has no asset %q", ErrNotFound, rel, name)
}

func checkPage(rel string) error {
	if rel == "" || path.Clean(rel) != rel || path.IsAbs(rel) || strings.HasPrefix(rel, "../") {
		return fmt.Errorf("%w: %q is not a clean content-relative path", ErrInvalid, rel)
	}
	if !strings.HasPrefix(rel, "wiki/") || !strings.HasSuffix(rel, ".md") {
		return fmt.Errorf("%w: only wiki/ pages can be exported, not %s", ErrInvalid, rel)
	}
	return nil
}

// splitFrontmatter returns the frontmatter's scalar fields as written, and
// the body.
func splitFrontmatter(src []byte) (map[string]string, []byte, error) {
	fields := map[string]string{}
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
			for k, n := range nodes {
				if n.Kind == yaml.ScalarNode {
					fields[k] = n.Value
				}
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

type exporter struct {
	root     *os.Root
	rel      string
	base     string
	assets   []Asset
	byPath   map[string]string // content path -> asset name
	names    map[string]bool
	warnings []string
	warned   map[string]bool
}

func (e *exporter) warn(format string, args ...any) {
	w := fmt.Sprintf(format, args...)
	if e.warned == nil {
		e.warned = map[string]bool{}
	}
	if !e.warned[w] {
		e.warned[w] = true
		e.warnings = append(e.warnings, w)
	}
}

var (
	titleLine = regexp.MustCompile(`^#\s+(.+?)\s*#*\s*$`)
	callout   = regexp.MustCompile(`^(\s*>\s*)\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*$`)
	videoEl   = regexp.MustCompile(`<video\b[^>]*>(?:\s*</video>)?`)
	attrRe    = regexp.MustCompile(`\b(src|title)="([^"]*)"`)
	imgEl     = regexp.MustCompile(`<img\b[^>]*>`)
	imgSrc    = regexp.MustCompile(`\bsrc="([^"]*)"`)
	jsxNum    = regexp.MustCompile(`\b(width|height)=\{(\d+)\}`)
	// A Markdown link or image: [text](target "title"). Text may hold one
	// level of brackets, as in a footnote's [link](..) inside quotes.
	mdLinkRe = regexp.MustCompile(`(!?)\[((?:[^\[\]]|\[[^\[\]]*\])*)\]\(([^)\s]+)((?:\s+"[^"]*")?)\)`)
	fenceRe  = regexp.MustCompile("^\\s{0,3}(`{3,}|~{3,})(.*)$")
	mathLine = regexp.MustCompile(`^\s*\$\$`)
)

// body rewrites a page body line by line, leaving fenced code alone.
func (e *exporter) body(body, title string) string {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	fence := ""
	droppedTitle := false
	for _, line := range lines {
		if m := fenceRe.FindStringSubmatch(line); m != nil {
			switch {
			case fence == "":
				fence = m[1]
				if strings.HasPrefix(strings.TrimSpace(m[2]), "mermaid") {
					e.warn("the page has a Mermaid diagram; the blog shows it as code unless its theme renders Mermaid")
				}
			case m[1][0] == fence[0] && len(m[1]) >= len(fence) && strings.TrimSpace(m[2]) == "":
				fence = ""
			}
			out = append(out, line)
			continue
		}
		if fence != "" {
			out = append(out, line)
			continue
		}
		if !droppedTitle && strings.TrimSpace(line) != "" {
			droppedTitle = true
			// The blog layout prints the title, so a matching heading goes.
			if m := titleLine.FindStringSubmatch(line); m != nil && strings.EqualFold(m[1], title) {
				continue
			}
		}
		if len(out) == 0 && strings.TrimSpace(line) == "" {
			continue
		}
		if mathLine.MatchString(line) {
			e.warn("the page has display math; the blog shows it as source unless its theme renders math")
		}
		if m := callout.FindStringSubmatch(line); m != nil {
			line = m[1] + "**" + titleCase(m[2]) + ":**"
		}
		out = append(out, e.line(line))
	}
	return strings.Join(out, "\n")
}

func titleCase(s string) string { return s[:1] + strings.ToLower(s[1:]) }

// line rewrites the links, images and HTML in one line. Code spans are
// masked first, so a link written inside one stays as written while a link
// whose label holds one is still rewritten.
func (e *exporter) line(line string) string {
	var masked strings.Builder
	var spans []string
	for len(line) > 0 {
		i := strings.IndexByte(line, '`')
		if i < 0 {
			masked.WriteString(line)
			break
		}
		masked.WriteString(line[:i])
		line = line[i:]
		n := len(line) - len(strings.TrimLeft(line, "`"))
		end := closingTicks(line[n:], n)
		if end < 0 {
			// An unmatched run of backticks is literal text.
			masked.WriteString(line[:n])
			line = line[n:]
			continue
		}
		masked.WriteString(codeMark + strconv.Itoa(len(spans)) + codeMark)
		spans = append(spans, line[:n+end+n])
		line = line[n+end+n:]
	}
	out := e.text(masked.String())
	return codeSpanRef.ReplaceAllStringFunc(out, func(m string) string {
		i, _ := strconv.Atoi(strings.Trim(m, codeMark))
		return spans[i]
	})
}

// codeMark brackets a masked code span's index; NUL never occurs in a page.
const codeMark = "\x00"

var codeSpanRef = regexp.MustCompile("\x00[0-9]+\x00")

// closingTicks finds a run of exactly n backticks in s and returns its
// index, or -1.
func closingTicks(s string, n int) int {
	for i := 0; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		j := i
		for j < len(s) && s[j] == '`' {
			j++
		}
		if j-i == n {
			return i
		}
		i = j
	}
	return -1
}

func (e *exporter) text(s string) string {
	s = videoEl.ReplaceAllStringFunc(s, e.video)
	s = imgEl.ReplaceAllStringFunc(s, e.img)
	return mdLinkRe.ReplaceAllStringFunc(s, e.link)
}

func (e *exporter) video(el string) string {
	title := "a video"
	for _, m := range attrRe.FindAllStringSubmatch(el, -1) {
		if m[1] == "title" && m[2] != "" {
			title = m[2]
		}
	}
	e.warn("a video (%s) is left out: the media store is LAN-only", title)
	return "*Video not included: " + strings.TrimSuffix(title, ".") + ".*"
}

func (e *exporter) img(el string) string {
	el = jsxNum.ReplaceAllString(el, `$1="$2"`)
	m := imgSrc.FindStringSubmatchIndex(el)
	if m == nil {
		return el
	}
	target := el[m[2]:m[3]]
	url, ok := e.file(target, true)
	if !ok {
		return ""
	}
	return el[:m[2]] + url + el[m[3]:]
}

func (e *exporter) link(l string) string {
	m := mdLinkRe.FindStringSubmatch(l)
	image, text, target, title := m[1] == "!", m[2], m[3], m[4]
	if isExternal(target) {
		return l
	}
	if image {
		url, ok := e.file(target, true)
		if !ok {
			return ""
		}
		return "![" + text + "](" + url + title + ")"
	}
	resolved, ok := resolve(e.rel, target)
	if !ok {
		return text
	}
	switch {
	case strings.HasPrefix(resolved, "sources/") && strings.HasSuffix(resolved, ".md"):
		if u := e.sourceURL(resolved); u != "" {
			return "[" + text + "](" + u + title + ")"
		}
		e.warn("%s has no source_url, so its citation link is left as text", resolved)
		return text
	case strings.HasSuffix(resolved, ".md"):
		// Other wiki pages and trails are LAN-only; the export is one page.
		return text
	}
	if url, ok := e.file(target, false); ok {
		return "[" + text + "](" + url + title + ")"
	}
	return text
}

func isExternal(target string) bool {
	return strings.Contains(target, "://") || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "mailto:")
}

// file makes a content file the page uses into an asset and returns its
// published URL. It reports false, with a warning, when the file cannot go
// with the page.
func (e *exporter) file(target string, image bool) (string, bool) {
	if isExternal(target) {
		return target, true
	}
	if media.RefRe.MatchString(target) {
		e.warn("%s is in the media store, which is LAN-only, so it is left out", target)
		return "", false
	}
	resolved, ok := resolve(e.rel, target)
	if !ok {
		e.warn("%s is outside the content repository, so it is left out", target)
		return "", false
	}
	if name, ok := e.byPath[resolved]; ok {
		return e.base + name, true
	}
	t, ok := media.TypeOf(resolved)
	if !ok || (image && t.Kind != media.Image) || t.Kind == media.Video {
		e.warn("%s is not a file type the export carries, so it is left out", resolved)
		return "", false
	}
	b, err := e.root.ReadFile(resolved)
	if err != nil {
		e.warn("%s does not exist, so it is left out", resolved)
		return "", false
	}
	name := e.uniqueName(path.Base(resolved))
	e.byPath[resolved] = name
	e.assets = append(e.assets, Asset{Name: name, Path: resolved, SHA256: media.Hash(b), Size: int64(len(b)), ContentType: t.MIME})
	return e.base + name, true
}

func (e *exporter) uniqueName(name string) string {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 2; e.names[name]; i++ {
		name = fmt.Sprintf("%s-%d%s", stem, i, ext)
	}
	e.names[name] = true
	return name
}

var urlEscaper = strings.NewReplacer("(", "%28", ")", "%29", " ", "%20")

func (e *exporter) sourceURL(rel string) string {
	src, err := e.root.ReadFile(rel)
	if err != nil {
		return ""
	}
	fields, _, err := splitFrontmatter(src)
	if err != nil {
		return ""
	}
	u := fields["source_url"]
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return ""
	}
	// Wikipedia-style "Loom_(video_game)" would end the Markdown link early.
	return urlEscaper.Replace(u)
}

// resolve turns a link target into a content-root-relative path, dropping
// any query or fragment. Targets outside the content root report false.
func resolve(fromRel, target string) (string, bool) {
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
