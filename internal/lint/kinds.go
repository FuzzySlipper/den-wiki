package lint

import "strings"

// Trail kinds. A trail's type names its kind, except that type trail (or no
// type) is a research run. The ledger uses the same names for job kinds.
const (
	KindResearch       = "research"
	KindRoundSynthesis = "round-synthesis"
	KindCalibration    = "calibration"
	KindCuration       = "curation"
)

// TrailKinds lists every trail kind with what it means.
var TrailKinds = []struct{ Kind, Meaning string }{
	{KindResearch, "A research run: one question, its sources and the pages it changed (type: trail)"},
	{KindRoundSynthesis, "The synthesis of a research round: the librarian's reading of the round's slice trails"},
	{KindCalibration, "One prompt given to several harnesses to compare them; read-only, its reports captured as agent-output sources"},
	{KindCuration, "A librarian curation pass"},
}

// legacyKindPrefixes infer a kind from the slug of a trail written before
// trails carried their kind in type. Trails are append-only, so those keep
// type: trail for good.
var legacyKindPrefixes = []struct{ prefix, kind string }{
	{"curation-", KindCuration},
	{"calibration-", KindCalibration},
}

// IsTrailKind reports whether kind is one of TrailKinds.
func IsTrailKind(kind string) bool {
	for _, k := range TrailKinds {
		if k.Kind == kind {
			return true
		}
	}
	return false
}

// TrailKind returns the kind of the trail with this slug and frontmatter:
// its type when that names a kind, else the kind its slug prefix implies
// (curation-, calibration-), else research.
func TrailKind(slug string, fields map[string]any) string {
	if t, _ := stringField(fields, "type"); t != "trail" && t != KindResearch && IsTrailKind(t) {
		return t
	}
	for _, p := range legacyKindPrefixes {
		if strings.HasPrefix(slug, p.prefix) {
			return p.kind
		}
	}
	return KindResearch
}

// isTrailType reports whether a frontmatter type belongs to a trail.
func isTrailType(t any) bool {
	s, _ := t.(string)
	return s == "trail" || s == KindRoundSynthesis || s == KindCalibration || s == KindCuration
}

// SourceTypeAgentOutput marks a source captured from one of our own agents'
// runs, such as a harness's calibration report. It records the run; it is
// not external evidence.
const SourceTypeAgentOutput = "agent-output"

// IsAgentOutput reports whether a source's frontmatter marks it as agent
// output.
func IsAgentOutput(fields map[string]any) bool {
	t, _ := stringField(fields, "type")
	return t == SourceTypeAgentOutput
}
