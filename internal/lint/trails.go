package lint

import (
	"fmt"
	"sort"
	"strings"
)

// TrailEntry is one trail as a listing shows it.
type TrailEntry struct {
	Path     string `json:"path"`
	Slug     string `json:"slug"`
	Kind     string `json:"kind"`
	Question string `json:"question"`
	Started  string `json:"started,omitempty"`
	Finished string `json:"finished,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Harness  string `json:"harness,omitempty"`
	Job      string `json:"job,omitempty"`
}

// TrailListing is the trails of one kind, or of every kind, newest first,
// with a Markdown table for hub pages and trails.
type TrailListing struct {
	Kind     string         `json:"kind,omitempty"`
	Counts   map[string]int `json:"counts"`
	Trails   []TrailEntry   `json:"trails"`
	Markdown string         `json:"listing_markdown"`
}

// ListTrails lists the trails under root of the given kind (every kind when
// kind is empty), newest started first. Counts covers every kind, so a
// caller filtering one kind still sees the others' totals.
func ListTrails(root, kind string) (TrailListing, error) {
	if kind != "" && !IsTrailKind(kind) {
		return TrailListing{}, fmt.Errorf("unknown trail kind %q (want %s)", kind, kindNames())
	}
	docs, err := loadZone(root, zoneTrails)
	if err != nil {
		return TrailListing{}, err
	}
	l := TrailListing{Kind: kind, Counts: map[string]int{}, Trails: []TrailEntry{}}
	for _, k := range TrailKinds {
		l.Counts[k.Kind] = 0
	}
	for _, d := range docs {
		e := TrailEntry{Path: d.rel, Slug: trailSlug(d.rel), Kind: kindOf(d)}
		l.Counts[e.Kind]++
		if kind != "" && e.Kind != kind {
			continue
		}
		e.Question, _ = stringField(d.fields, "question")
		e.Started = scalar(d.fields["started"])
		e.Finished = scalar(d.fields["finished"])
		e.Agent, _ = stringField(d.fields, "agent")
		e.Harness, _ = stringField(d.fields, "harness")
		e.Job = scalar(d.fields["job"])
		l.Trails = append(l.Trails, e)
	}
	sort.SliceStable(l.Trails, func(i, j int) bool {
		a, b := l.Trails[i], l.Trails[j]
		if a.Started != b.Started {
			return a.Started > b.Started
		}
		return a.Slug < b.Slug
	})
	l.Markdown = renderTrails(l)
	return l, nil
}

func kindNames() string {
	var names []string
	for _, k := range TrailKinds {
		names = append(names, k.Kind)
	}
	return strings.Join(names, ", ")
}

// scalar renders a frontmatter value that may be a string, number or date.
func scalar(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func renderTrails(l TrailListing) string {
	if len(l.Trails) == 0 {
		return "None.\n"
	}
	var b strings.Builder
	b.WriteString("| Trail | Kind | Started | Finished | Agent (harness) | Question |\n|---|---|---|---|---|---|\n")
	cell := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ") }
	date := func(s string) string {
		if len(s) >= 10 {
			return s[:10]
		}
		return s
	}
	for _, e := range l.Trails {
		finished := date(e.Finished)
		if finished == "" {
			finished = "running"
		}
		fmt.Fprintf(&b, "| [%s](/%s) | %s | %s | %s | %s (%s) | %s |\n",
			cell(e.Slug), e.Path, e.Kind, date(e.Started), finished, cell(e.Agent), cell(e.Harness), cell(e.Question))
	}
	return b.String()
}
