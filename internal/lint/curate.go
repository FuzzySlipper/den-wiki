package lint

import (
	"bytes"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Curation classes, in the order the librarian works through them. Like lint
// codes they are never renamed or reused.
const (
	ClassDuplicate        = "duplicate"
	ClassNaming           = "naming"
	ClassDeadLink         = "dead_link"
	ClassOrphan           = "orphan"
	ClassMissingHub       = "missing_hub"
	ClassUnfoldedProposal = "unfolded_proposal"
	ClassStale            = "stale"
	ClassOversize         = "oversize"
	ClassSource           = "source"
	ClassUnreviewed       = "unreviewed"
)

// CurationClasses lists every class with what it means.
var CurationClasses = []struct{ Class, Meaning string }{
	{ClassDuplicate, "Two wiki pages whose titles, aliases or names are the same or nearly the same: merge them, or tell them apart with aliases"},
	{ClassNaming, "A wiki page or folder name breaks the naming rule: move it to a compliant name"},
	{ClassDeadLink, "A wiki page or trail links to a page that does not exist (from OpenKnowledge)"},
	{ClassOrphan, "A wiki page that no page links to (from OpenKnowledge)"},
	{ClassMissingHub, "A tag carried by enough pages to need a hub page, with no hub page for it"},
	{ClassUnfoldedProposal, "A research trail's \"Proposed changes\" that no curation trail has handled yet"},
	{ClassStale, "A page older than its volatility half-life since it was last updated or verified"},
	{ClassOversize, "A page whose body is long enough to split"},
	{ClassSource, "A source that fails its hash, names a missing trail or superseded file, or that no page cites"},
	{ClassUnreviewed, "A page still marked curation: unreviewed"},
}

// HalfLifeDays maps a page's volatility to the age at which it counts as stale.
var HalfLifeDays = map[string]int{"hot": 30, "warm": 180, "cold": 730}

// DeadLink is one broken link as OpenKnowledge reports it, content-root
// relative.
type DeadLink struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// CurateOptions configures a curation scan. DeadLinks and Orphans come from
// OpenKnowledge's link views; the rest is read from the content directory.
type CurateOptions struct {
	Now       time.Time
	DeadLinks []DeadLink
	// Orphans are content-root-relative .md paths nothing links to.
	Orphans []string
	// OversizeWords is the body length that counts as oversize; default 2500.
	OversizeWords int
	// HubMinPages is how many pages a tag needs before it wants a hub;
	// default 5.
	HubMinPages int
	// DuplicateSimilarity is the name similarity (0 to 1) at which two pages
	// count as near-duplicates; default 0.6.
	DuplicateSimilarity float64
}

// CurationItem is one thing for the librarian to look at. Score orders items
// within a class: higher is more urgent.
type CurationItem struct {
	Class   string   `json:"class"`
	Path    string   `json:"path"`
	Related []string `json:"related,omitempty"`
	Score   float64  `json:"score"`
	Message string   `json:"message"`
}

// CurationReport is the machine-readable result of a scan, with a rendered
// summary for people and trail pages.
type CurationReport struct {
	Generated string         `json:"generated"`
	Pages     int            `json:"pages"`
	Trails    int            `json:"trails"`
	Sources   int            `json:"sources"`
	Counts    map[string]int `json:"counts"`
	Items     []CurationItem `json:"items"`
	Summary   string         `json:"summary_markdown"`
}

type corpusDoc struct {
	rel    string
	fields map[string]any
	body   []byte
}

// Curate scans the content directory for curation work. It scores and
// reports; it never changes anything.
func Curate(root string, opts CurateOptions) (CurationReport, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if opts.OversizeWords <= 0 {
		opts.OversizeWords = 2500
	}
	if opts.HubMinPages <= 0 {
		opts.HubMinPages = 5
	}
	if opts.DuplicateSimilarity <= 0 {
		opts.DuplicateSimilarity = 0.6
	}
	findings, err := Lint(root, Options{})
	if err != nil {
		return CurationReport{}, err
	}
	pages, err := loadZone(root, zoneWiki)
	if err != nil {
		return CurationReport{}, err
	}
	trails, err := loadZone(root, zoneTrails)
	if err != nil {
		return CurationReport{}, err
	}
	sources, err := loadZone(root, zoneSources)
	if err != nil {
		return CurationReport{}, err
	}
	c := curator{opts: opts, pages: pages, trails: trails, sources: sources}
	c.duplicates()
	c.fromLint(findings)
	c.links()
	c.missingHubs()
	c.unfoldedProposals()
	c.staleness()
	c.oversize()
	c.uncitedSources()
	c.unreviewed()

	sort.SliceStable(c.items, func(i, j int) bool {
		a, b := c.items[i], c.items[j]
		if ca, cb := classRank(a.Class), classRank(b.Class); ca != cb {
			return ca < cb
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		return a.Path < b.Path
	})
	counts := map[string]int{}
	for _, cl := range CurationClasses {
		counts[cl.Class] = 0
	}
	for _, it := range c.items {
		counts[it.Class]++
	}
	r := CurationReport{
		Generated: opts.Now.UTC().Format(time.RFC3339),
		Pages:     len(pages), Trails: len(trails), Sources: len(sources),
		Counts: counts, Items: c.items,
	}
	if r.Items == nil {
		r.Items = []CurationItem{}
	}
	r.Summary = renderSummary(r)
	return r, nil
}

func classRank(class string) int {
	for i, cl := range CurationClasses {
		if cl.Class == class {
			return i
		}
	}
	return len(CurationClasses)
}

// loadZone reads every document in a zone, skipping generated indexes,
// dot-folders and OpenKnowledge asset folders, as lint does.
func loadZone(root, zone string) ([]corpusDoc, error) {
	dir := filepath.Join(root, zone)
	var docs []corpusDoc
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir && os.IsNotExist(err) {
				return filepath.SkipDir
			}
			return err
		}
		name := d.Name()
		if p != dir && (strings.HasPrefix(name, ".") || (d.IsDir() && strings.HasSuffix(name, assetsSuffix))) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || name == indexName || !strings.HasSuffix(name, ".md") {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		relOS, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		doc, err := parseDocument(src)
		if err != nil || !doc.hasFrontmatter {
			// Lint reports these; curation works with what parses.
			doc = document{fields: map[string]any{}, body: src}
		}
		docs = append(docs, corpusDoc{rel: filepath.ToSlash(relOS), fields: doc.fields, body: doc.body})
		return nil
	})
	return docs, err
}

type curator struct {
	opts                   CurateOptions
	pages, trails, sources []corpusDoc
	items                  []CurationItem
}

func (c *curator) add(it CurationItem) { c.items = append(c.items, it) }

// --- duplicates ---

var stopWords = map[string]bool{"a": true, "an": true, "the": true, "of": true, "and": true, "or": true, "for": true, "in": true, "on": true, "to": true, "with": true, "vs": true, "versus": true}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// nameTokens reduces a title, alias or file name to a set of comparable
// words: lower case, punctuation and stop words dropped, simple plurals
// folded.
func nameTokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range nonWord.Split(strings.ToLower(s), -1) {
		if w == "" || stopWords[w] {
			continue
		}
		if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
			w = strings.TrimSuffix(w, "s")
		}
		out[w] = true
	}
	return out
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for w := range a {
		if b[w] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

func pageNames(d corpusDoc) []string {
	names := []string{strings.TrimSuffix(path.Base(d.rel), ".md")}
	if t, ok := stringField(d.fields, "title"); ok {
		names = append(names, t)
	}
	return append(names, stringList(d.fields, "aliases")...)
}

func (c *curator) duplicates() {
	type named struct {
		doc    corpusDoc
		tokens []map[string]bool
		labels []string
	}
	var ns []named
	for _, p := range c.pages {
		n := named{doc: p}
		for _, name := range pageNames(p) {
			if t := nameTokens(name); len(t) > 0 {
				n.tokens = append(n.tokens, t)
				n.labels = append(n.labels, name)
			}
		}
		ns = append(ns, n)
	}
	for i := range ns {
		for j := i + 1; j < len(ns); j++ {
			best, la, lb := 0.0, "", ""
			for x, ta := range ns[i].tokens {
				for y, tb := range ns[j].tokens {
					if s := jaccard(ta, tb); s > best {
						best, la, lb = s, ns[i].labels[x], ns[j].labels[y]
					}
				}
			}
			if best < c.opts.DuplicateSimilarity {
				continue
			}
			kind := "near-duplicate"
			if best == 1 {
				kind = "same name"
			}
			c.add(CurationItem{
				Class: ClassDuplicate, Path: ns[i].doc.rel, Related: []string{ns[j].doc.rel}, Score: round2(best),
				Message: fmt.Sprintf("%s: %q and %q (similarity %.2f); merge them if they are one concept, otherwise add aliases that tell them apart", kind, la, lb, best),
			})
		}
	}
}

// --- lint-derived classes: naming and source integrity ---

func (c *curator) fromLint(fs []Finding) {
	for _, f := range fs {
		switch {
		case strings.HasPrefix(f.Code, "naming."):
			c.add(CurationItem{Class: ClassNaming, Path: f.Path, Score: 1, Message: f.Code + ": " + f.Message})
		case f.Code == "source.sha256_mismatch" || f.Code == "source.supersedes_missing",
			f.Code == "ref.missing_trail" && strings.HasPrefix(f.Path, zoneSources+"/"):
			c.add(CurationItem{Class: ClassSource, Path: f.Path, Score: 1, Message: f.Code + ": " + f.Message})
		}
	}
}

// --- links (from OpenKnowledge) ---

func (c *curator) links() {
	for _, d := range c.opts.DeadLinks {
		// Captured sources keep their original relative links; those are
		// expected to dangle.
		if strings.HasPrefix(d.Source, zoneSources+"/") {
			continue
		}
		c.add(CurationItem{Class: ClassDeadLink, Path: d.Source, Related: []string{d.Target}, Score: 1, Message: "links to " + d.Target + ", which does not exist; point it at the right page or remove the link"})
	}
	for _, o := range c.opts.Orphans {
		// Trails and sources are reached through page frontmatter, which is
		// not a link; only pages need inbound links.
		if !strings.HasPrefix(o, zoneWiki+"/") {
			continue
		}
		c.add(CurationItem{Class: ClassOrphan, Path: o, Score: 1, Message: "no page links here; link it from related pages or a hub"})
	}
}

// --- missing hubs ---

func (c *curator) missingHubs() {
	tagged := map[string][]string{}
	hubbed := map[string]bool{}
	for _, p := range c.pages {
		t, _ := stringField(p.fields, "type")
		for _, tag := range stringList(p.fields, "tags") {
			if t == "hub" {
				hubbed[tag] = true
				continue
			}
			tagged[tag] = append(tagged[tag], p.rel)
		}
	}
	for tag, ps := range tagged {
		if hubbed[tag] || len(ps) < c.opts.HubMinPages {
			continue
		}
		sort.Strings(ps)
		c.add(CurationItem{
			Class: ClassMissingHub, Path: "tag:" + tag, Related: ps, Score: float64(len(ps)),
			Message: fmt.Sprintf("%d pages carry tag %q and no hub page does; add a type: hub page with that tag linking them", len(ps), tag),
		})
	}
}

// --- trails' proposed changes ---

var trailLink = regexp.MustCompile(`\]\((?:/|\.\./|\./)?(?:trails/)?([a-z0-9][a-z0-9._-]*)\.md(?:#[^)]*)?\)`)

// sectionText returns the body text under a "## <name>" heading, up to the
// next "## " heading.
func sectionText(body []byte, name string) string {
	var out []string
	in := false
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "## ") {
			in = normaliseHeading(strings.TrimPrefix(line, "## ")) == normaliseHeading(name)
			continue
		}
		if in {
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func isCurationTrail(rel string) bool {
	return strings.HasPrefix(path.Base(rel), "curation-")
}

func (c *curator) unfoldedProposals() {
	handled := map[string]bool{}
	for _, t := range c.trails {
		if !isCurationTrail(t.rel) {
			continue
		}
		for _, m := range trailLink.FindAllSubmatch(t.body, -1) {
			handled[string(m[1])] = true
		}
	}
	for _, t := range c.trails {
		if isCurationTrail(t.rel) {
			continue
		}
		slug := strings.TrimSuffix(path.Base(t.rel), ".md")
		text := sectionText(t.body, "Proposed changes")
		if handled[slug] || text == "" || isNone(text) {
			continue
		}
		c.add(CurationItem{
			Class: ClassUnfoldedProposal, Path: t.rel, Score: 1,
			Message: "proposed changes not yet handled: " + firstLine(text) + " (fold or decline them, and link this trail from the curation trail)",
		})
	}
}

var noneText = regexp.MustCompile(`(?i)^none[.!]?\s*$`)

func isNone(text string) bool {
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !noneText.MatchString(l) {
			return false
		}
	}
	return true
}

func firstLine(text string) string {
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "-*"))
		if l != "" && !noneText.MatchString(l) {
			if len(l) > 160 {
				l = l[:157] + "..."
			}
			return l
		}
	}
	return ""
}

// --- staleness ---

func parseDate(v any) (time.Time, bool) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func (c *curator) staleness() {
	for _, p := range c.pages {
		vol, _ := stringField(p.fields, "volatility")
		if vol == "" {
			vol = "warm"
		}
		half, ok := HalfLifeDays[vol]
		if !ok {
			continue
		}
		since, field := time.Time{}, ""
		for _, k := range []string{"verified", "updated"} {
			if t, ok := parseDate(p.fields[k]); ok && t.After(since) {
				since, field = t, k
			}
		}
		if since.IsZero() {
			continue
		}
		age := int(c.opts.Now.Sub(since).Hours() / 24)
		if age < half {
			continue
		}
		c.add(CurationItem{
			Class: ClassStale, Path: p.rel, Score: round2(float64(age) / float64(half)),
			Message: fmt.Sprintf("%s %d days ago (%s), past the %d-day half-life for %s pages; recheck it or queue a research job", field, age, since.Format("2006-01-02"), half, vol),
		})
	}
}

// --- oversize ---

func (c *curator) oversize() {
	for _, p := range c.pages {
		words := len(bytes.Fields(p.body))
		if words <= c.opts.OversizeWords {
			continue
		}
		c.add(CurationItem{
			Class: ClassOversize, Path: p.rel, Score: round2(float64(words) / float64(c.opts.OversizeWords)),
			Message: fmt.Sprintf("%d words (limit %d); split it into linked pages", words, c.opts.OversizeWords),
		})
	}
}

// --- uncited sources ---

func (c *curator) uncitedSources() {
	cited := map[string]bool{}
	superseded := map[string]bool{}
	for _, p := range c.pages {
		for _, s := range stringList(p.fields, "sources") {
			cited[path.Clean(strings.TrimPrefix(s, "/"))] = true
		}
		for _, m := range mdLink.FindAllSubmatch(p.body, -1) {
			target := string(m[1])
			if i := strings.IndexAny(target, "#?"); i >= 0 {
				target = target[:i]
			}
			if strings.HasPrefix(target, "/") {
				cited[path.Clean(strings.TrimPrefix(target, "/"))] = true
			} else if !strings.Contains(target, "://") {
				cited[path.Clean(path.Join(path.Dir(p.rel), target))] = true
			}
		}
	}
	for _, s := range c.sources {
		if old, ok := stringField(s.fields, "supersedes"); ok {
			superseded[path.Clean(strings.TrimPrefix(old, "/"))] = true
		}
	}
	for _, s := range c.sources {
		if cited[s.rel] || superseded[s.rel] {
			continue
		}
		c.add(CurationItem{Class: ClassSource, Path: s.rel, Score: 0.5, Message: "no page cites this source; cite it where it supports a claim, or leave it for later research"})
	}
}

// --- unreviewed backlog ---

func (c *curator) unreviewed() {
	for _, p := range c.pages {
		if cur, _ := stringField(p.fields, "curation"); cur != "unreviewed" {
			continue
		}
		age := 0
		if t, ok := parseDate(p.fields["updated"]); ok {
			age = int(c.opts.Now.Sub(t).Hours() / 24)
		}
		c.add(CurationItem{
			Class: ClassUnreviewed, Path: p.rel, Score: float64(age),
			Message: fmt.Sprintf("unreviewed, last updated %d days ago; review it against its sources and promote it to curated", age),
		})
	}
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

// --- summary ---

func renderSummary(r CurationReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Curation scan %s: %d pages, %d trails, %d sources.\n\n", r.Generated, r.Pages, r.Trails, r.Sources)
	b.WriteString("| Class | Items |\n|---|---|\n")
	for _, cl := range CurationClasses {
		fmt.Fprintf(&b, "| %s | %d |\n", cl.Class, r.Counts[cl.Class])
	}
	for _, cl := range CurationClasses {
		if r.Counts[cl.Class] == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n### %s\n\n", cl.Class)
		for _, it := range r.Items {
			if it.Class != cl.Class {
				continue
			}
			line := "- `" + it.Path + "`"
			if len(it.Related) > 0 {
				line += " (" + strings.Join(it.Related, ", ") + ")"
			}
			b.WriteString(line + ": " + it.Message + "\n")
		}
	}
	return b.String()
}
