// Package lint checks a den-wiki content directory against the content
// contract in docs/content-contract.md. It reports findings; it never changes
// content, and no finding blocks a write.
package lint

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Zones, in the order the contract lists them.
const (
	zoneSources = "sources"
	zoneTrails  = "trails"
	zoneWiki    = "wiki"
)

var zones = []string{zoneSources, zoneTrails, zoneWiki}

// Root-level files the contract allows besides the zones.
const (
	rootIndex    = "index.md"
	taxonomyFile = "taxonomy.yml"
	indexName    = "index.md"
	zoneRulesRel = ".ok/frontmatter.yml"
	assetsSuffix = ".assets"
)

// TrailSections are the H2 sections every trail page carries, in order.
var TrailSections = []string{
	"Question and scope",
	"Prior coverage checked",
	"Queries and methods",
	"Sources",
	"Dead ends",
	"Changes made",
	"Proposed changes",
	"Open questions and gaps",
}

// Options selects optional checks.
type Options struct {
	// Commits is a git revision range (for example main~10..main). When set,
	// lint also checks the commits in it; Root must be inside a git work tree.
	Commits string
}

// Lint checks the content directory at root.
func Lint(root string, opts Options) ([]Finding, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	sch, err := compileSchemas()
	if err != nil {
		return nil, fmt.Errorf("compile schemas: %w", err)
	}
	l := &linter{root: root, schemas: sch, names: map[string][]string{}}
	if err := l.run(); err != nil {
		return nil, err
	}
	if opts.Commits != "" {
		cf, err := l.checkCommits(opts.Commits)
		if err != nil {
			return nil, err
		}
		l.out = append(l.out, cf...)
	}
	sortFindings(l.out)
	return l.out, nil
}

type linter struct {
	root     string
	schemas  *schemaSet
	taxonomy *taxonomy
	out      []Finding
	// names maps a normalised title or alias to the wiki pages that claim it.
	names map[string][]string
}

func (l *linter) add(f Finding) { l.out = append(l.out, f) }

func (l *linter) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(l.root, filepath.FromSlash(rel)))
	return err == nil
}

func (l *linter) run() error {
	l.taxonomy = l.loadTaxonomy()

	entries, err := os.ReadDir(l.root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasPrefix(name, "."):
			// .git, .ok, .gitignore and other tooling files are not content.
		case e.IsDir() && isZone(name):
		case !e.IsDir() && (name == rootIndex || name == taxonomyFile):
		default:
			l.add(newFinding(codeZoneOutside, name, "content belongs in sources/, trails/ or wiki/; only index.md and taxonomy.yml sit at the root"))
		}
	}
	if err := l.checkIndex(rootIndex, true); err != nil {
		return err
	}
	for _, z := range zones {
		if err := l.walkZone(z); err != nil {
			return err
		}
	}
	l.reportNameCollisions()
	return nil
}

func isZone(name string) bool {
	for _, z := range zones {
		if name == z {
			return true
		}
	}
	return false
}

func (l *linter) walkZone(zone string) error {
	dir := filepath.Join(l.root, zone)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		l.add(newFinding(codeZoneMissing, zone, "zone folder "+zone+"/ does not exist"))
		return nil
	}
	if !l.exists(zone + "/" + zoneRulesRel) {
		l.add(newFinding(codeZoneRulesMissing, zone, zone+"/"+zoneRulesRel+" is missing, so agents listing the folder do not see its rules"))
	}
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relOS, err := filepath.Rel(l.root, p)
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(relOS)
		name := d.Name()
		if p != dir && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() && strings.HasSuffix(name, assetsSuffix) {
			// OpenKnowledge keeps each document's pasted files in <doc>.assets/.
			return filepath.SkipDir
		}
		if d.IsDir() {
			if p != dir && zone == zoneWiki {
				l.out = append(l.out, checkWikiName(rel, name)...)
			}
			return nil
		}
		if name == indexName {
			return l.checkIndex(rel, false)
		}
		if !strings.HasSuffix(name, ".md") {
			l.add(newFinding(codeZoneNonMarkdown, rel, "not a Markdown file; zone rules only check .md files"))
			return nil
		}
		return l.checkFile(zone, rel)
	})
}

// checkIndex checks a generated index.md, which carries no frontmatter except
// okf_version at the content root.
func (l *linter) checkIndex(rel string, isRoot bool) error {
	src, err := os.ReadFile(filepath.Join(l.root, filepath.FromSlash(rel)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	doc, err := parseDocument(src)
	if err != nil {
		l.add(newFinding(codeIndexFrontmatter, rel, "index.md is generated and carries no frontmatter: "+err.Error()))
		return nil
	}
	for k := range doc.fields {
		if isRoot && k == "okf_version" {
			continue
		}
		l.add(newFinding(codeIndexFrontmatter, rel, "index.md is generated; remove frontmatter key "+quote(k)))
	}
	return nil
}

// zoneShape guesses which zone a frontmatter block was written for, from keys
// only that zone's schema has.
func zoneShape(fields map[string]any) string {
	has := func(k string) bool { _, ok := fields[k]; return ok }
	switch {
	case has("sha256") || has("source_url") || has("captured_by_trail"):
		return zoneSources
	case has("question") && (has("harness") || has("started") || has("job")):
		return zoneTrails
	case has("curation") || has("confidence") || has("volatility"):
		return zoneWiki
	}
	return ""
}

func (l *linter) checkFile(zone, rel string) error {
	src, err := os.ReadFile(filepath.Join(l.root, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	if zone == zoneWiki {
		l.out = append(l.out, checkWikiName(rel, strings.TrimSuffix(path.Base(rel), ".md"))...)
	}
	if zone == zoneTrails && strings.Contains(strings.TrimPrefix(rel, zoneTrails+"/"), "/") {
		l.add(newFinding(codeZoneNestedTrail, rel, "move the trail to trails/"+path.Base(rel)+"; a (trail:<slug>) tag names trails/<slug>.md"))
	}
	doc, err := parseDocument(src)
	if err != nil {
		l.add(newFinding(codeFrontmatterYAML, rel, err.Error()))
		return nil
	}
	if !doc.hasFrontmatter {
		l.add(newFinding(codeFrontmatterNone, rel, "start the file with a --- frontmatter block; see the "+zone+"/ template"))
		return nil
	}
	if shape := zoneShape(doc.fields); shape != "" && shape != zone {
		l.add(newFinding(codeZoneMisplaced, rel, "frontmatter is shaped for "+shape+"/ but the file is in "+zone+"/; move it, or fix its frontmatter"))
		return nil
	}
	sch, check := l.schemas.page, l.checkPage
	switch zone {
	case zoneSources:
		sch, check = l.schemas.source, l.checkSource
	case zoneTrails:
		sch, check = l.schemas.trail, l.checkTrail
	}
	errs, err := validate(sch, doc.fields)
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	for _, e := range errs {
		l.add(newFinding(codeFrontmatterSchema, rel, e))
	}
	check(rel, doc)
	return nil
}

// --- wiki/ pages ---

var relabelKeys = []string{"deprecated", "superseded", "superseded_by", "supersedes", "obsolete", "replaced_by", "status"}

var relabelTitle = regexp.MustCompile(`(?i)^\s*[\[(]?\s*(deprecated|obsolete|superseded|outdated|old)\b`)

func (l *linter) checkPage(rel string, doc document) {
	f := doc.fields
	if l.taxonomy != nil {
		if t, ok := stringField(f, "type"); ok && !l.taxonomy.types[t] {
			l.add(newFinding(codeUnknownType, rel, "type "+quote(t)+" is not in taxonomy.yml"))
		}
		for _, tag := range stringList(f, "tags") {
			if !l.taxonomy.tags[tag] {
				l.add(newFinding(codeUnknownTag, rel, "tag "+quote(tag)+" is not in taxonomy.yml; add it there first"))
			}
		}
	}
	for _, s := range stringList(f, "sources") {
		if strings.HasPrefix(s, zoneSources+"/") && !l.exists(s) {
			l.add(newFinding(codeRefSource, rel, "sources entry "+quote(s)+" does not exist"))
		}
	}
	for _, t := range stringList(f, "trails") {
		if slugRe.MatchString(t) && !l.exists(zoneTrails+"/"+t+".md") {
			l.add(newFinding(codeRefTrail, rel, "trails entry "+quote(t)+" has no trails/"+t+".md"))
		}
	}
	for _, c := range stringList(f, "contradicts") {
		if strings.HasPrefix(c, zoneWiki+"/") && !l.exists(c) {
			l.add(newFinding(codeRefPage, rel, "contradicts entry "+quote(c)+" does not exist"))
		}
	}
	for _, k := range relabelKeys {
		if _, ok := f[k]; ok {
			l.add(newFinding(codeWikiRelabelled, rel, "frontmatter key "+quote(k)+" relabels the page; merge it into the page that replaces it, or delete it"))
		}
	}
	title, _ := stringField(f, "title")
	if relabelTitle.MatchString(title) {
		l.add(newFinding(codeWikiRelabelled, rel, "title "+quote(title)+" labels the page as old; merge or delete it instead"))
	}
	names := append([]string{title}, stringList(f, "aliases")...)
	seen := map[string]bool{}
	for _, n := range names {
		key := strings.Join(strings.Fields(strings.ToLower(n)), " ")
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		l.names[key] = append(l.names[key], rel)
	}
	l.checkCitations(rel, doc)
}

func (l *linter) reportNameCollisions() {
	keys := make([]string, 0, len(l.names))
	for k := range l.names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		pages := l.names[k]
		if len(pages) < 2 {
			continue
		}
		sort.Strings(pages)
		for _, p := range pages[1:] {
			l.add(newFinding(codeWikiNameCollision, p, "title or alias "+quote(k)+" is also claimed by "+pages[0]+"; merge the pages"))
		}
	}
}

var (
	footnoteDef = regexp.MustCompile(`^\[\^[^\]]+\]:\s*(.*)$`)
	mdLink      = regexp.MustCompile(`\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	quotedSpan  = regexp.MustCompile(`"[^"]*\S[^"]*"|“[^”]*\S[^”]*”|(^|\s)>\s*\S`)
	fenceLine   = regexp.MustCompile("^\\s*(```|~~~)")
)

// checkCitations checks footnote definitions that link into sources/: the
// target must exist and the footnote must quote the supporting span.
func (l *linter) checkCitations(rel string, doc document) {
	lines := strings.Split(string(doc.body), "\n")
	inFence := false
	for i, line := range lines {
		if fenceLine.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		m := footnoteDef.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		text := m[1]
		// Footnote definitions may continue on indented lines.
		for j := i + 1; j < len(lines) && (strings.HasPrefix(lines[j], "    ") || strings.HasPrefix(lines[j], "\t")); j++ {
			text += " " + strings.TrimSpace(lines[j])
		}
		for _, lm := range mdLink.FindAllStringSubmatch(text, -1) {
			target, ok := l.resolveLink(rel, lm[1])
			if !ok || !strings.HasPrefix(target, zoneSources+"/") {
				continue
			}
			f := newFinding(codeCitationSource, rel, "citation links to "+target+", which does not exist")
			f.Line = doc.bodyLine + i
			if !l.exists(target) {
				l.add(f)
			}
			withoutLinks := mdLink.ReplaceAllString(text, "]")
			if !quotedSpan.MatchString(withoutLinks) {
				q := newFinding(codeCitationQuote, rel, "citation of "+target+" does not quote the supporting span; add it in \"double quotes\"")
				q.Line = doc.bodyLine + i
				l.add(q)
			}
		}
	}
}

// resolveLink turns a Markdown link target into a content-root-relative path.
// External links and anchors return false.
func (l *linter) resolveLink(fromRel, target string) (string, bool) {
	if strings.Contains(target, "://") || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "mailto:") {
		return "", false
	}
	if i := strings.IndexAny(target, "#?"); i >= 0 {
		target = target[:i]
	}
	var p string
	if strings.HasPrefix(target, "/") {
		p = path.Clean(strings.TrimPrefix(target, "/"))
	} else {
		p = path.Clean(path.Join(path.Dir(fromRel), target))
	}
	if strings.HasPrefix(p, "../") || p == ".." {
		return "", false
	}
	return p, true
}

// --- sources/ ---

func (l *linter) checkSource(rel string, doc document) {
	f := doc.fields
	if want, ok := stringField(f, "sha256"); ok {
		if got := bodySHA256(doc.body); got != strings.ToLower(want) {
			l.add(newFinding(codeSourceHash, rel, "body sha256 is "+got+" but frontmatter says "+want+"; sources are never edited, so capture a new file if the content changed"))
		}
	}
	if s, ok := stringField(f, "supersedes"); ok {
		switch {
		case s == rel:
			l.add(newFinding(codeSourceSupersedes, rel, "supersedes names the file itself"))
		case !l.exists(s):
			l.add(newFinding(codeSourceSupersedes, rel, "supersedes target "+quote(s)+" does not exist"))
		}
	}
	if t, ok := stringField(f, "captured_by_trail"); ok && slugRe.MatchString(t) && !l.exists(zoneTrails+"/"+t+".md") {
		l.add(newFinding(codeRefTrail, rel, "captured_by_trail "+quote(t)+" has no trails/"+t+".md"))
	}
}

// --- trails/ ---

var (
	h2           = regexp.MustCompile(`^##\s+(.+?)\s*#*\s*$`)
	headingNoise = regexp.MustCompile(`^[0-9]+[.)]?\s*|[\s:.]+$`)
)

func normaliseHeading(s string) string {
	s = headingNoise.ReplaceAllString(strings.TrimSpace(s), "")
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

func (l *linter) checkTrail(rel string, doc document) {
	have := map[string]bool{}
	inFence := false
	for _, line := range strings.Split(string(doc.body), "\n") {
		if fenceLine.MatchString(line) {
			inFence = !inFence
			continue
		}
		if m := h2.FindStringSubmatch(line); m != nil && !inFence {
			have[normaliseHeading(m[1])] = true
		}
	}
	for _, s := range TrailSections {
		if !have[normaliseHeading(s)] {
			l.add(newFinding(codeTrailSection, rel, "missing section \"## "+s+"\""))
		}
	}
}
