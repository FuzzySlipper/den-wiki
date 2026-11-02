package lint

import "sort"

// Rule describes one stable finding code. docs/content-contract.md lists every
// code; a test keeps the two in step.
type Rule struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Summary  string   `json:"summary"`
}

var rules = map[string]Rule{}

func rule(code string, sev Severity, summary string) string {
	if _, dup := rules[code]; dup {
		panic("duplicate lint rule " + code)
	}
	rules[code] = Rule{Code: code, Severity: sev, Summary: summary}
	return code
}

// Rule codes. Never rename or reuse a code; add a new one instead.
var (
	codeZoneOutside       = rule("zone.outside", Warning, "a top-level entry is not a zone, index.md or taxonomy.yml")
	codeZoneMissing       = rule("zone.missing", Info, "a zone folder (sources/, trails/, wiki/) does not exist")
	codeZoneRulesMissing  = rule("zone.rules_missing", Info, "a zone folder has no .ok/frontmatter.yml stating its rules")
	codeZoneMisplaced     = rule("zone.misplaced", Warning, "a file's frontmatter belongs to a different zone than the folder it is in")
	codeZoneNonMarkdown   = rule("zone.non_markdown", Info, "a zone holds a file that is not Markdown, so no other rule checks it")
	codeZoneNestedTrail   = rule("zone.nested_trail", Warning, "a trail is in a subfolder; trails are flat so a slug names one file")
	codeNamingSlug        = rule("naming.slug", Warning, "a name under wiki/ is not a lowercase slug")
	codeNamingDate        = rule("naming.date", Warning, "a name under wiki/ contains a date")
	codeNamingSHA         = rule("naming.sha", Warning, "a name under wiki/ contains what looks like a commit SHA or hash")
	codeNamingVersion     = rule("naming.version_marker", Warning, "a name under wiki/ carries a version or status marker such as -v2, -final or -old")
	codeFrontmatterNone   = rule("frontmatter.missing", Error, "a page, source or trail has no frontmatter block")
	codeFrontmatterYAML   = rule("frontmatter.invalid", Error, "a frontmatter block is not a YAML mapping")
	codeFrontmatterSchema = rule("frontmatter.schema", Error, "frontmatter does not match its zone's JSON Schema")
	codeIndexFrontmatter  = rule("index.frontmatter", Warning, "a generated index.md carries frontmatter (only okf_version, at the root, is allowed)")
	codeTaxonomyMissing   = rule("taxonomy.missing", Error, "taxonomy.yml is missing, so tags and types cannot be checked")
	codeTaxonomyInvalid   = rule("taxonomy.invalid", Error, "taxonomy.yml does not match its schema")
	codeTaxonomyDuplicate = rule("taxonomy.duplicate", Warning, "taxonomy.yml lists a tag or type twice")
	codeTaxonomyParent    = rule("taxonomy.unknown_parent", Warning, "a taxonomy tag names a parent that is not a tag")
	codeUnknownTag        = rule("taxonomy.unknown_tag", Warning, "a page uses a tag that is not in taxonomy.yml")
	codeUnknownType       = rule("taxonomy.unknown_type", Warning, "a page uses a type that is not in taxonomy.yml")
	codeRefSource         = rule("ref.missing_source", Warning, "a page's sources entry names a file that does not exist")
	codeRefTrail          = rule("ref.missing_trail", Warning, "a trails or captured_by_trail entry names a trail that does not exist")
	codeRefPage           = rule("ref.missing_page", Warning, "a contradicts entry names a wiki/ page that does not exist")
	codeWikiRelabelled    = rule("wiki.relabelled", Warning, "a page is labelled deprecated, superseded or old instead of being deleted or merged")
	codeWikiNameCollision = rule("wiki.name_collision", Warning, "two pages share a title or alias, so one concept has two pages")
	codeCitationSource    = rule("citation.missing_source", Warning, "a citation footnote links to a sources/ file that does not exist")
	codeCitationQuote     = rule("citation.missing_quote", Warning, "a citation footnote links to a source without quoting the supporting span")
	codeCitationOneWord   = rule("citation.one_word_text", Warning, "a citation footnote starts with one-word link text, which OpenKnowledge misreads")
	codeSourceHash        = rule("source.sha256_mismatch", Error, "a source's body does not match its sha256 field")
	codeSourceSupersedes  = rule("source.supersedes_missing", Warning, "a source's supersedes target does not exist")
	codeSourceMedia       = rule("source.media_missing", Warning, "a source's media file does not exist")
	codeSourceMediaHash   = rule("source.media_sha256_mismatch", Error, "a source's media file does not match its media_sha256")
	codeMediaMissing      = rule("media.missing", Warning, "a page, source or trail refers to a /media/ file the media store does not hold")
	codeTrailSection      = rule("trail.section_missing", Warning, "a trail lacks one of the required sections")
	codeCommitTrailTag    = rule("commit.missing_trail_tag", Warning, "a commit changes wiki/ without (trail:<slug>), (ui) or (ui:format) in its subject")
	codeCommitTrailFound  = rule("commit.unknown_trail", Warning, "a commit's (trail:<slug>) names a trail that does not exist")
	codeCommitSourceEdit  = rule("commit.source_edited", Warning, "a commit modifies an existing source; a re-fetch is a new file with supersedes")
	codeCommitTrailEdit   = rule("commit.trail_rewritten", Warning, "a commit removes lines from or deletes a trail; trails are append-only")
)

// Rules returns every rule, sorted by code.
func Rules() []Rule {
	out := make([]Rule, 0, len(rules))
	for _, r := range rules {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

func newFinding(code, path, msg string) Finding {
	r, ok := rules[code]
	if !ok {
		panic("unregistered lint rule " + code)
	}
	return Finding{Code: code, Severity: r.Severity, Path: path, Message: msg}
}
