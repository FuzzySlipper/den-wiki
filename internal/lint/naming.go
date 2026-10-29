package lint

import (
	"regexp"
	"strings"
)

var (
	slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	// Dates in any common shape: 2026-10-29, 2026_10_29, 20261029, 2026-10, q3-2026.
	dateRe = regexp.MustCompile(`(?i)((19|20)\d{2}[-_.]?(0[1-9]|1[0-2])([-_.]?(0[1-9]|[12]\d|3[01]))?)|((^|[-_.])q[1-4][-_.]?(19|20)\d{2})|((19|20)\d{2}[-_.]?q[1-4]($|[-_.]))`)
	hexRe  = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
	digit  = regexp.MustCompile(`[0-9]`)
	letter = regexp.MustCompile(`[a-f]`)
)

// versionMarkers label a version or a status instead of naming the concept.
// They count only as the last token, so "new-york" or "old-norse" pass while
// "agents-new" does not. History lives in git, so none belong in wiki/ names.
var versionMarkers = map[string]bool{
	"final": true, "new": true, "updated": true, "old": true, "latest": true,
	"backup": true, "bak": true, "deprecated": true, "obsolete": true,
	"superseded": true, "wip": true, "tmp": true, "temp": true, "orig": true,
	"revised": true,
}

// versionToken matches v2, rev3 and r10 in any position.
var versionToken = regexp.MustCompile(`^(v|rev|r)\d+$`)

// checkWikiName returns findings for one file or folder name under wiki/.
// name excludes any .md extension; rel is the path findings report.
func checkWikiName(rel, name string) []Finding {
	var out []Finding
	if !slugRe.MatchString(name) {
		out = append(out, newFinding(codeNamingSlug, rel, "name "+quote(name)+" is not a lowercase slug (a-z, 0-9, single hyphens)"))
	}
	if dateRe.MatchString(name) {
		out = append(out, newFinding(codeNamingDate, rel, "name "+quote(name)+" contains a date; wiki/ names say what a page is about, git says when"))
	}
	tokens := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return r == '-' || r == '_' || r == '.' || r == ' ' })
	for _, t := range tokens {
		if hexRe.MatchString(t) && digit.MatchString(t) && letter.MatchString(t) {
			out = append(out, newFinding(codeNamingSHA, rel, "name "+quote(name)+" contains "+quote(t)+", which looks like a SHA or hash"))
			break
		}
	}
	for i, t := range tokens {
		if versionToken.MatchString(t) || (i == len(tokens)-1 && versionMarkers[t]) {
			out = append(out, newFinding(codeNamingVersion, rel, "name "+quote(name)+" carries the marker "+quote(t)+"; edit the one page in place, git keeps the versions"))
			break
		}
	}
	return out
}

func quote(s string) string { return "\"" + s + "\"" }
