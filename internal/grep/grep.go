// Package grep finds passages in the content without reading whole files:
// matching lines with their context and the headings they sit under, and
// single sections by heading (#10756). OpenKnowledge's search ranks pages;
// this answers "where exactly does it say X" so an agent can read that part
// alone.
package grep

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/FuzzySlipper/den-wiki/internal/content"
)

// ErrInvalid is a bad pattern or scope; ErrNotFound a scope that names
// nothing.
var (
	ErrInvalid  = errors.New("invalid")
	ErrNotFound = errors.New("not found")
)

// Zones are the content folders a whole-content search covers.
var Zones = []string{"wiki", "sources", "trails"}

// Scope narrows a search. At most one field is set; none searches every zone.
type Scope struct {
	Path   string // one page, source or trail
	Folder string // a folder and everything under it
	Topic  string // the wiki pages naming this topic or one of its subtopics
	Zone   string // wiki, sources or trails
}

// Options say what to match and how much to return.
type Options struct {
	Pattern       string
	Regex         bool // RE2 syntax; otherwise the pattern is literal text
	CaseSensitive bool
	Context       int // lines before and after each match
	PerFile       int // matches per file; 0 means DefaultPerFile
	Max           int // matches in all; 0 means DefaultMax
}

// Defaults for Options.
const (
	DefaultPerFile = 10
	DefaultMax     = 50
	// maxLine bounds a returned line; a long paragraph is cut around the
	// match.
	maxLine = 400
)

// Line is one returned line, numbered from 1 in the file.
type Line struct {
	N    int    `json:"n"`
	Text string `json:"text"`
}

// Passage is one run of matches with their context, under one heading.
type Passage struct {
	Path string `json:"path"`
	// Heading is the heading path the first match sits under, outermost
	// first, e.g. "Combat › Stamina and Fatigue"; empty above the first
	// heading. wiki_read's section takes its last part.
	Heading string `json:"heading,omitempty"`
	// Hits are the matching line numbers; Lines adds the context.
	Hits  []int  `json:"hits"`
	Lines []Line `json:"lines"`
}

// Result is a search's passages: wiki/, then sources/, then trails/, each
// in path order.
type Result struct {
	Passages []Passage `json:"passages"`
	Matches  int       `json:"matches"`
	Files    int       `json:"files_matched"`
	Searched int       `json:"files_searched"`
	// Truncated says a per-file or total limit cut matches off.
	Truncated bool `json:"truncated"`
}

// Search finds opts.Pattern in the files scope covers under root.
func Search(root string, scope Scope, opts Options) (Result, error) {
	res := Result{Passages: []Passage{}}
	re, err := compile(opts)
	if err != nil {
		return res, err
	}
	if opts.PerFile <= 0 {
		opts.PerFile = DefaultPerFile
	}
	if opts.Max <= 0 {
		opts.Max = DefaultMax
	}
	if opts.Context < 0 {
		opts.Context = 0
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return res, err
	}
	defer r.Close()
	files, err := scopeFiles(r, scope)
	if err != nil {
		return res, err
	}
	for _, rel := range files {
		src, err := r.ReadFile(rel)
		if err != nil {
			continue
		}
		res.Searched++
		if res.Matches >= opts.Max {
			if re.Match(src) {
				res.Truncated = true
			}
			continue
		}
		ps, n, cut := searchFile(rel, string(src), re, opts, opts.Max-res.Matches)
		if n > 0 {
			res.Files++
			res.Matches += n
			res.Passages = append(res.Passages, ps...)
		}
		res.Truncated = res.Truncated || cut
	}
	return res, nil
}

func compile(opts Options) (*regexp.Regexp, error) {
	if strings.TrimSpace(opts.Pattern) == "" {
		return nil, fmt.Errorf("%w: pattern is empty", ErrInvalid)
	}
	p := opts.Pattern
	if !opts.Regex {
		p = regexp.QuoteMeta(p)
	}
	if !opts.CaseSensitive {
		p = "(?i)" + p
	}
	re, err := regexp.Compile(p)
	if err != nil {
		return nil, fmt.Errorf("%w: pattern: %v", ErrInvalid, err)
	}
	return re, nil
}

// searchFile returns the file's passages, how many lines matched, and
// whether a limit cut some off.
func searchFile(rel, src string, re *regexp.Regexp, opts Options, room int) ([]Passage, int, bool) {
	lines := strings.Split(strings.TrimSuffix(src, "\n"), "\n")
	heads := headings(lines)
	limit := min(opts.PerFile, room)
	var hits []int
	cut := false
	for i, l := range lines {
		if re.MatchString(l) {
			if len(hits) == limit {
				cut = true
				break
			}
			hits = append(hits, i)
		}
	}
	var out []Passage
	end := -1 // last line index the current passage covers
	for _, h := range hits {
		from, to := max(0, h-opts.Context), min(len(lines)-1, h+opts.Context)
		if len(out) > 0 && from <= end+1 {
			p := &out[len(out)-1]
			p.Hits = append(p.Hits, h+1)
			for i := end + 1; i <= to; i++ {
				p.Lines = append(p.Lines, line(i, lines[i], re))
			}
			end = max(end, to)
			continue
		}
		p := Passage{Path: rel, Heading: heads[h], Hits: []int{h + 1}}
		for i := from; i <= to; i++ {
			p.Lines = append(p.Lines, line(i, lines[i], re))
		}
		out = append(out, p)
		end = to
	}
	return out, len(hits), cut
}

// line returns line i, cut to maxLine runes around its first match.
func line(i int, text string, re *regexp.Regexp) Line {
	r := []rune(text)
	if len(r) <= maxLine {
		return Line{N: i + 1, Text: text}
	}
	start := 0
	if loc := re.FindStringIndex(text); loc != nil {
		start = max(0, len([]rune(text[:loc[0]]))-maxLine/2)
	}
	stop := min(len(r), start+maxLine)
	start = max(0, stop-maxLine)
	s := string(r[start:stop])
	if start > 0 {
		s = "…" + s
	}
	if stop < len(r) {
		s += "…"
	}
	return Line{N: i + 1, Text: s}
}

// headings returns, for each line, the heading path it sits under. Headings
// inside fenced code and the frontmatter do not count.
func headings(lines []string) []string {
	out := make([]string, len(lines))
	var stack []heading
	var f fence
	start := frontmatterEnd(lines)
	for i, l := range lines {
		if i >= start && !f.step(l) {
			if h, ok := parseHeading(l); ok {
				for len(stack) > 0 && stack[len(stack)-1].level >= h.level {
					stack = stack[:len(stack)-1]
				}
				stack = append(stack, h)
			}
		}
		parts := make([]string, len(stack))
		for j, h := range stack {
			parts[j] = h.text
		}
		out[i] = strings.Join(parts, " › ")
	}
	return out
}

type heading struct {
	level int
	text  string
}

var headingRe = regexp.MustCompile(`^(#{1,6})[ \t]+(.*?)[ \t]*#*[ \t]*$`)

func parseHeading(l string) (heading, bool) {
	m := headingRe.FindStringSubmatch(l)
	if m == nil || strings.TrimSpace(m[2]) == "" {
		return heading{}, false
	}
	return heading{level: len(m[1]), text: strings.TrimSpace(m[2])}, true
}

// fence tracks fenced code blocks line by line.
type fence struct {
	char byte
	size int
}

// step reports whether l is inside a fence or is one of its delimiters.
func (f *fence) step(l string) bool {
	t := strings.TrimLeft(l, " ")
	if len(l)-len(t) > 3 {
		return f.size > 0
	}
	n := 0
	for n < len(t) && (t[n] == '`' || t[n] == '~') && t[n] == t[0] {
		n++
	}
	if f.size > 0 {
		if n >= f.size && t[0] == f.char && strings.TrimSpace(t[n:]) == "" {
			f.size = 0
		}
		return true
	}
	if n >= 3 {
		f.char, f.size = t[0], n
		return true
	}
	return false
}

// frontmatterEnd returns the index of the first line after the frontmatter.
func frontmatterEnd(lines []string) int {
	if len(lines) == 0 || lines[0] != "---" {
		return 0
	}
	for i := 1; i < len(lines); i++ {
		if l := strings.TrimSuffix(lines[i], "\r"); l == "---" || l == "..." {
			return i + 1
		}
	}
	return 0
}

// Section returns the heading named name (case-insensitive, the heading's
// own text) with everything under it, up to the next heading of the same or
// a higher level, and its heading path. ok is false when no heading matches;
// headings then lists the file's headings.
func Section(src, name string) (text, path string, ok bool, all []string) {
	lines := strings.Split(strings.TrimSuffix(src, "\n"), "\n")
	heads := headings(lines)
	want := strings.ToLower(strings.TrimSpace(name))
	var f fence
	start := frontmatterEnd(lines)
	from, level := -1, 0
	for i := start; i < len(lines); i++ {
		if f.step(lines[i]) {
			continue
		}
		h, isHead := parseHeading(lines[i])
		if !isHead {
			continue
		}
		if from >= 0 {
			if h.level <= level {
				return strings.Join(lines[from:i], "\n") + "\n", heads[from], true, nil
			}
			continue
		}
		all = append(all, h.text)
		if strings.ToLower(h.text) == want {
			from, level = i, h.level
		}
	}
	if from >= 0 {
		return strings.Join(lines[from:], "\n") + "\n", heads[from], true, nil
	}
	return "", "", false, all
}

// scopeFiles lists the .md files scope covers, sorted.
func scopeFiles(r *os.Root, s Scope) ([]string, error) {
	set := 0
	for _, v := range []string{s.Path, s.Folder, s.Topic, s.Zone} {
		if v != "" {
			set++
		}
	}
	if set > 1 {
		return nil, fmt.Errorf("%w: give one of path, folder, topic or zone", ErrInvalid)
	}
	switch {
	case s.Path != "":
		rel := strings.TrimPrefix(s.Path, "/")
		if !content.Clean(rel) || !strings.HasSuffix(rel, ".md") || !inZone(rel) {
			return nil, fmt.Errorf("%w: path %q is not a content .md file", ErrInvalid, s.Path)
		}
		if st, err := r.Stat(rel); err != nil || st.IsDir() {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, rel)
		}
		return []string{rel}, nil
	case s.Folder != "":
		dir := strings.Trim(s.Folder, "/")
		if !content.Clean(dir) || !inZone(dir) {
			return nil, fmt.Errorf("%w: folder %q is not inside wiki/, sources/ or trails/", ErrInvalid, s.Folder)
		}
		if st, err := r.Stat(dir); err != nil || !st.IsDir() {
			return nil, fmt.Errorf("%w: folder %s", ErrNotFound, dir)
		}
		return walk(r, dir)
	case s.Zone != "":
		if !inZone(s.Zone) || strings.Contains(s.Zone, "/") {
			return nil, fmt.Errorf("%w: zone %q (want wiki, sources or trails)", ErrInvalid, s.Zone)
		}
		return walk(r, s.Zone)
	case s.Topic != "":
		return topicFiles(r, s.Topic)
	}
	var all []string
	for _, z := range Zones {
		fs, err := walk(r, z)
		if err != nil {
			return nil, err
		}
		all = append(all, fs...)
	}
	return all, nil
}

func inZone(rel string) bool {
	for _, z := range Zones {
		if rel == z || strings.HasPrefix(rel, z+"/") {
			return true
		}
	}
	return false
}

// walk lists the .md files under dir, leaving out tool folders, assets and
// generated indexes.
func walk(r *os.Root, dir string) ([]string, error) {
	var out []string
	err := fs.WalkDir(r.FS(), dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				return fs.SkipAll
			}
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p != dir && (strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".assets")) {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".md") && name != "index.md" {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// topicFiles lists the wiki pages naming topic or one of its subtopics in
// taxonomy.yml.
func topicFiles(r *os.Root, topic string) ([]string, error) {
	var tax struct {
		Topics []struct {
			Name   string `yaml:"name"`
			Parent string `yaml:"parent"`
		} `yaml:"topics"`
	}
	if src, err := r.ReadFile("taxonomy.yml"); err == nil {
		_ = yaml.Unmarshal(src, &tax)
	}
	want := map[string]bool{}
	for _, t := range tax.Topics {
		if t.Name == topic {
			want[topic] = true
		}
	}
	if !want[topic] {
		return nil, fmt.Errorf("%w: topic %q is not in taxonomy.yml; wiki_taxonomy lists the topics", ErrNotFound, topic)
	}
	// Add subtopics until nothing changes; a parent cycle cannot loop
	// because each name is added once.
	for grew := true; grew; {
		grew = false
		for _, t := range tax.Topics {
			if !want[t.Name] && want[t.Parent] {
				want[t.Name], grew = true, true
			}
		}
	}
	pages, err := walk(r, "wiki")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, rel := range pages {
		src, err := r.ReadFile(rel)
		if err != nil {
			continue
		}
		fm, _, err := content.Split(src)
		if err == nil && want[fm.Str("topic")] {
			out = append(out, rel)
		}
	}
	return out, nil
}
