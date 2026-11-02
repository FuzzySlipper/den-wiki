package lint

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func writeContent(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const curateTaxonomy = `version: 1
types:
  - {name: concept, description: c}
  - {name: hub, description: h}
tags:
  - {name: software, description: s}
  - {name: games, description: g}
`

func curPage(title, extra, body string) string {
	return "---\ntype: concept\ntitle: " + title + "\ntags: [software]\ncuration: unreviewed\nupdated: 2026-10-01\n" + extra + "---\n\n" + body + "\n"
}

func curTrail(question, proposed string) string {
	return "---\ntype: trail\nquestion: " + question + "\nstarted: 2026-10-01T00:00:00Z\nagent: a\nharness: codex\n---\n\n" +
		"## Question and scope\n\nq\n\n## Prior coverage checked\n\nNone.\n\n## Queries and methods\n\nNone.\n\n## Sources\n\nNone.\n\n" +
		"## Dead ends\n\nNone.\n\n## Changes made\n\nNone.\n\n## Proposed changes\n\n" + proposed + "\n\n## Open questions and gaps\n\nNone.\n"
}

// curateFixture seeds one instance of every curation class, plus pages that
// must not be reported.
func curateFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	long := strings.Repeat("word ", 40)
	writeContent(t, root, map[string]string{
		"taxonomy.yml": curateTaxonomy,
		// duplicate: near-identical titles
		"wiki/vector-databases.md":  curPage("Vector databases", "", "about vectors"),
		"wiki/vector-database.md":   curPage("The vector database", "", "also about vectors"),
		"wiki/agent-memory.md":      curPage("Agent memory", "volatility: cold\n", "unrelated"),
		"wiki/old-notes-2024-01.md": curPage("Notes about caching", "", "dated name"), // naming
		"wiki/hot-topic.md":         curPage("Hot topic", "volatility: hot\n", "fast moving"),
		"wiki/huge-page.md":         curPage("Huge page", "", long),
		"wiki/reviewed.md":          strings.Replace(curPage("Reviewed page", "", "fine"), "curation: unreviewed", "curation: curated", 1),
		// four more software pages make five, so software wants a hub; games has one page
		"wiki/game-loop.md": strings.Replace(curPage("Game loop", "", "x"), "[software]", "[games]", 1),
		// sources: one cited, one uncited, one with a bad hash
		"sources/cited.md":   "---\ntype: source\nsource_url: https://e.x/a\ncaptured: 2026-10-01\nsha256: \"" + bodySHA256([]byte("a\n")) + "\"\ncaptured_by_trail: research-one\n---\na\n",
		"sources/uncited.md": "---\ntype: source\nsource_url: https://e.x/b\ncaptured: 2026-10-01\nsha256: \"" + bodySHA256([]byte("b\n")) + "\"\ncaptured_by_trail: research-one\n---\nb\n",
		"sources/badhash.md": "---\ntype: source\nsource_url: https://e.x/c\ncaptured: 2026-10-01\nsha256: \"" + strings.Repeat("0", 64) + "\"\ncaptured_by_trail: research-one\n---\nc\n",
		// trails: one with open proposals, one whose proposals a curation trail handled, one with none
		"trails/research-one.md":   curTrail("one", "- Add a hub for software."),
		"trails/research-two.md":   curTrail("two", "- Merge vector pages."),
		"trails/research-three.md": curTrail("three", "None."),
		// research-two is handled in Changes made; research-one is only
		// referenced under Prior coverage checked, which does not count.
		"trails/curation-2026-10-02-test.md": strings.Replace(strings.Replace(curTrail("curation", "None."),
			"## Changes made\n\nNone.", "## Changes made\n\n- Folded proposals from [research two](/trails/research-two.md).", 1),
			"## Prior coverage checked\n\nNone.", "## Prior coverage checked\n\n- [research one](/trails/research-one.md)", 1),
	})
	// agent-memory cites the cited source so only uncited.md is reported
	writeContent(t, root, map[string]string{
		"wiki/agent-memory.md": curPage("Agent memory", "volatility: cold\n", "unrelated.[^1]\n\n[^1]: [The cited source](/sources/cited.md) \"a\""),
	})
	return root
}

func itemsOf(r CurationReport, class string) []CurationItem {
	var out []CurationItem
	for _, it := range r.Items {
		if it.Class == class {
			out = append(out, it)
		}
	}
	return out
}

func TestCurateFindsEveryClass(t *testing.T) {
	root := curateFixture(t)
	now := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	r, err := Curate(root, CurateOptions{
		Now:           now,
		OversizeWords: 30,
		DeadLinks:     []DeadLink{{Source: "wiki/agent-memory.md", Target: "wiki/missing"}, {Source: "sources/cited.md", Target: "docs/x"}},
		Orphans:       []string{"wiki/hot-topic.md", "trails/research-one.md"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		ClassDuplicate:        {"wiki/vector-database.md"},
		ClassNaming:           {"wiki/old-notes-2024-01.md"},
		ClassDeadLink:         {"wiki/agent-memory.md"},
		ClassOrphan:           {"wiki/hot-topic.md"},
		ClassMissingHub:       {"tag:software"},
		ClassUnfoldedProposal: {"trails/research-one.md"},
		ClassStale:            {"wiki/hot-topic.md"},
		ClassOversize:         {"wiki/huge-page.md"},
		ClassSource:           {"sources/badhash.md", "sources/badhash.md", "sources/uncited.md"}, // bad hash, and uncited
	}
	for class, paths := range want {
		got := itemsOf(r, class)
		var gotPaths []string
		for _, it := range got {
			gotPaths = append(gotPaths, it.Path)
		}
		if strings.Join(gotPaths, ",") != strings.Join(paths, ",") {
			t.Errorf("%s: got %v, want %v", class, gotPaths, paths)
		}
	}
	if n := r.Counts[ClassUnreviewed]; n != 7 {
		t.Errorf("unreviewed: %d, want 7 (every page but reviewed.md)", n)
	}
	for _, it := range itemsOf(r, ClassUnreviewed) {
		if it.Path == "wiki/reviewed.md" {
			t.Errorf("a curated page is in the unreviewed backlog")
		}
	}
	if d := itemsOf(r, ClassDuplicate); len(d) == 1 && d[0].Related[0] != "wiki/vector-databases.md" {
		t.Errorf("duplicate pair: %+v", d[0])
	}
	if len(r.Counts) != len(CurationClasses) {
		t.Errorf("counts should list every class: %v", r.Counts)
	}
	if !strings.Contains(r.Summary, "| duplicate | 1 |") || !strings.Contains(r.Summary, "### stale") {
		t.Errorf("summary:\n%s", r.Summary)
	}
}

func TestCurateCleanContentReportsNothing(t *testing.T) {
	root := t.TempDir()
	writeContent(t, root, map[string]string{
		"taxonomy.yml":       curateTaxonomy,
		"wiki/one-thing.md":  strings.Replace(curPage("One thing", "", "body"), "curation: unreviewed", "curation: curated", 1),
		"trails/research.md": curTrail("q", "None."),
	})
	r, err := Curate(root, CurateOptions{Now: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 0 {
		t.Fatalf("clean content has items: %+v", r.Items)
	}
}

func TestStalenessUsesTheLaterOfUpdatedAndVerified(t *testing.T) {
	root := t.TempDir()
	writeContent(t, root, map[string]string{
		"taxonomy.yml":    curateTaxonomy,
		"wiki/checked.md": curPage("Checked", "volatility: hot\nverified: 2026-11-25\n", "x"),
	})
	r, err := Curate(root, CurateOptions{Now: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if n := r.Counts[ClassStale]; n != 0 {
		t.Fatalf("a page verified six days ago is stale: %+v", itemsOf(r, ClassStale))
	}
}

func TestNameTokensFoldPluralsAndStopWords(t *testing.T) {
	a, b := nameTokens("The Vector Databases"), nameTokens("vector-database")
	if jaccard(a, b) != 1 {
		t.Fatalf("tokens %v and %v should match", a, b)
	}
	if jaccard(nameTokens("Agent memory"), nameTokens("Game loop")) != 0 {
		t.Fatal("unrelated names should not overlap")
	}
}

// A curation decision covers the proposals a research trail held when the
// decision was committed; proposals appended later are reported again.
func TestUnfoldedProposalsAppendedAfterACurationDecision(t *testing.T) {
	root := t.TempDir()
	gitRun(t, root, "init", "-q", "-b", "main")
	decision := func(slug, line string) string {
		return strings.Replace(curTrail("curation", "None."), "## Changes made\n\nNone.", "## Changes made\n\n- "+line+" [research](/trails/"+slug+".md).", 1)
	}
	writeContent(t, root, map[string]string{
		"taxonomy.yml":           curateTaxonomy,
		"trails/research-log.md": curTrail("log", "- Proposal A."),
	})
	commitAll(t, root, "seed")
	scan := func() []CurationItem {
		t.Helper()
		r, err := Curate(root, CurateOptions{Now: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)})
		if err != nil {
			t.Fatal(err)
		}
		var out []CurationItem
		for _, it := range r.Items {
			if it.Class == ClassUnfoldedProposal {
				out = append(out, it)
			}
		}
		return out
	}
	if got := scan(); len(got) != 1 || !strings.Contains(got[0].Message, "Proposal A") {
		t.Fatalf("before any decision: %+v", got)
	}
	writeContent(t, root, map[string]string{"trails/curation-2026-10-02-a.md": decision("research-log", "Declined proposal A in")})
	commitAll(t, root, "decide A")
	if got := scan(); len(got) != 0 {
		t.Fatalf("after deciding A: %+v", got)
	}

	// The research trail grows a proposal after the decision.
	writeContent(t, root, map[string]string{"trails/research-log.md": curTrail("log", "- Proposal A.\n- Proposal B.")})
	commitAll(t, root, "append B")
	got := scan()
	if len(got) != 1 || !strings.Contains(got[0].Message, "1 proposal line(s) added") || !strings.Contains(got[0].Message, "Proposal B") || strings.Contains(got[0].Message, "Proposal A") {
		t.Fatalf("after appending B: %+v", got)
	}

	pendingB := func(when string) {
		t.Helper()
		got := scan()
		if len(got) != 1 || got[0].Path != "trails/research-log.md" || !strings.Contains(got[0].Message, "Proposal B") {
			t.Fatalf("%s, B should still be pending: %+v", when, got)
		}
	}

	// A later reference to the research trail outside Changes made, or a
	// reworded decision, is not a decision on B (R10468-2).
	decidedA := decision("research-log", "Declined proposal A in")
	writeContent(t, root, map[string]string{"trails/curation-2026-10-02-a.md": strings.Replace(decidedA, "## Open questions and gaps\n\nNone.", "## Open questions and gaps\n\n- Revisit [research](/trails/research-log.md).", 1)})
	commitAll(t, root, "mention research-log elsewhere")
	pendingB("after a reference outside Changes made")
	writeContent(t, root, map[string]string{"trails/curation-2026-10-02-a.md": strings.Replace(decidedA, "Declined proposal A in", "Declined proposal A (duplicate) in", 1)})
	commitAll(t, root, "reword decision A")
	pendingB("after rewording decision A")

	// An uncommitted decision on another research trail covers only that
	// trail; research-log keeps its committed snapshot (R10468-3).
	writeContent(t, root, map[string]string{"trails/research-other.md": curTrail("other", "- Proposal X.")})
	commitAll(t, root, "add research-other")
	writeContent(t, root, map[string]string{"trails/curation-2026-10-03-x.md": decision("research-other", "Declined proposal X in")})
	pendingB("with an uncommitted decision on another trail")
	commitAll(t, root, "decide X")

	// A second decision, in a later curation trail, covers B.
	writeContent(t, root, map[string]string{"trails/curation-2026-10-03-b.md": decision("research-log", "Declined proposal B in")})
	commitAll(t, root, "decide B")
	if got := scan(); len(got) != 0 {
		t.Fatalf("after deciding B: %+v", got)
	}

	// A decision added to a committed curation trail but not committed yet
	// covers the research trail as it is now.
	writeContent(t, root, map[string]string{"trails/research-log.md": curTrail("log", "- Proposal A.\n- Proposal B.\n- Proposal C.")})
	commitAll(t, root, "append C")
	if got := scan(); len(got) != 1 || !strings.Contains(got[0].Message, "Proposal C") {
		t.Fatalf("after appending C: %+v", got)
	}
	writeContent(t, root, map[string]string{"trails/curation-2026-10-03-b.md": strings.Replace(decision("research-log", "Declined proposal B in"), "## Changes made\n\n", "## Changes made\n\n- Declined proposal C in [research](/trails/research-log.md).\n", 1)})
	if got := scan(); len(got) != 0 {
		t.Fatalf("with C decided but not committed: %+v", got)
	}
}

// Trail kinds steer the scan: calibration runs propose nothing for the
// librarian and the pages only they changed are records, agent output is
// not uncited evidence, and a curation trail is known by its type as well
// as by its slug.
func TestCurateHonoursTrailKinds(t *testing.T) {
	root := t.TempDir()
	kinded := func(kind, proposed string) string {
		return strings.Replace(curTrail(kind, proposed), "type: trail", "type: "+kind, 1)
	}
	agentOut := "---\ntype: agent-output\nsource_url: file://den-agents/run/report.md\ncaptured: 2026-10-01\nsha256: \"" + bodySHA256([]byte("r\n")) + "\"\ncaptured_by_trail: cal-two\n---\nr\n"
	writeContent(t, root, map[string]string{
		"taxonomy.yml":                  curateTaxonomy,
		"trails/cal-two.md":             kinded("calibration", "- Give DSH a longer timeout."),
		"trails/calibration-01-old.md":  curTrail("legacy calibration", "- Tune the launcher."),
		"trails/war-round-synthesis.md": kinded("round-synthesis", "- Merge the war pages."),
		"trails/research-folded.md":     curTrail("folded", "- Add a page."),
		"trails/nightly.md":             strings.Replace(kinded("curation", "None."), "## Changes made\n\nNone.", "## Changes made\n\n- Folded [research](/trails/research-folded.md).", 1),
		"sources/calibration/report.md": agentOut,
		"wiki/calibration-runs.md":      strings.Replace(curPage("Calibration runs", "trails: [cal-two, calibration-01-old]\n", "runs"), "type: concept", "type: hub", 1),
		"wiki/harness-notes.md":         curPage("Harness notes", "trails: [cal-two, research-folded]\n", "notes"),
		"wiki/plain-unreviewed-page.md": curPage("Plain unreviewed page", "", "x"),
	})
	r, err := Curate(root, CurateOptions{Now: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	paths := func(class string) string {
		var out []string
		for _, it := range itemsOf(r, class) {
			out = append(out, it.Path)
		}
		return strings.Join(out, ",")
	}
	if got := paths(ClassUnfoldedProposal); got != "trails/war-round-synthesis.md" {
		t.Errorf("unfolded proposals: %q, want only the round synthesis", got)
	}
	if got := paths(ClassSource); got != "" {
		t.Errorf("source items: %q, want none for uncited agent output", got)
	}
	if got := paths(ClassUnreviewed); got != "wiki/harness-notes.md,wiki/plain-unreviewed-page.md" {
		t.Errorf("unreviewed: %q, want every page but the calibration hub", got)
	}
}

// Oversize counts prose only, and gives a page in sections more room unless
// one section is itself too long.
func TestOversizeAccountsForStructure(t *testing.T) {
	words := func(n int) string { return strings.TrimSpace(strings.Repeat("word ", n)) }
	section := func(name string, n int) string { return "## " + name + "\n\n" + words(n) + "\n\n" }
	structure := "```mermaid\n" + strings.Repeat("A --> B\n", 200) + "```\n\n| a | b |\n|---|---|\n| " + words(300) + " | x |\n\n$$\n" + words(300) + "\n$$\n\n<video src=\"/media/x.mp4\" />\n\n![" + words(50) + "](./x.assets/a.png)\n\n[^1]: " + words(300) + "\n"
	root := t.TempDir()
	writeContent(t, root, map[string]string{
		"taxonomy.yml": curateTaxonomy,
		// 90 words of prose and a lot of structure: not oversize at 100.
		"wiki/rich-page.md": curPage("Rich page", "", words(90)+"\n\n"+structure),
		// 150 words in one block: oversize.
		"wiki/wall-of-text.md": curPage("Wall of text", "", words(150)),
		// 180 words over three sections of 60: under twice the limit.
		"wiki/sectioned-page.md": curPage("Sectioned page", "", section("One", 60)+section("Two", 60)+section("Three", 60)),
		// Three sections, one of 120: that section is over the limit.
		"wiki/lopsided-page.md": curPage("Lopsided page", "", section("One", 20)+section("Two", 120)+section("Three", 20)),
	})
	r, err := Curate(root, CurateOptions{Now: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), OversizeWords: 100})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range itemsOf(r, ClassOversize) {
		got = append(got, it.Path)
	}
	if strings.Join(got, ",") != "wiki/wall-of-text.md,wiki/lopsided-page.md" {
		t.Fatalf("oversize: %v", got)
	}
}

// A page's sources are its citation footnotes: a source named only in a
// leftover sources field, linked in running text, or cited in a fenced example
// is uncited; a footnote that links on a continuation line cites.
func TestUncitedSourcesIgnoreTheSourcesField(t *testing.T) {
	root := t.TempDir()
	src := func(body string) string {
		return "---\ntype: source\nsource_url: https://e.x/" + strings.TrimSpace(body) + "\ncaptured: 2026-10-01\nsha256: \"" + bodySHA256([]byte(body)) + "\"\ncaptured_by_trail: research-one\n---\n" + body
	}
	body := "A claim.[^1] Another.[^2] See [the linked source](../sources/linked-only.md).\n\n" +
		"```markdown\n[^9]: [An example citation](../sources/fenced-only.md) \"e\"\n```\n\n" +
		"[^1]: [The footnoted source](../sources/footnoted.md) \"f\"\n" +
		"[^2]: A long citation that continues\n    on [the next line](/sources/continued.md) \"c\""
	writeContent(t, root, map[string]string{
		"taxonomy.yml":           curateTaxonomy,
		"trails/research-one.md": curTrail("one", "None."),
		"sources/footnoted.md":   src("f\n"),
		"sources/continued.md":   src("c\n"),
		"sources/listed-only.md": src("l\n"),
		"sources/linked-only.md": src("i\n"),
		"sources/fenced-only.md": src("e\n"),
		"wiki/page.md":           curPage("Page", "sources: [sources/footnoted.md, sources/listed-only.md]\n", body),
	})
	r, err := Curate(root, CurateOptions{Now: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	var uncited []string
	for _, it := range itemsOf(r, ClassSource) {
		uncited = append(uncited, it.Path)
	}
	sort.Strings(uncited)
	if got, want := strings.Join(uncited, ","), "sources/fenced-only.md,sources/linked-only.md,sources/listed-only.md"; got != want {
		t.Fatalf("uncited: %s, want %s", got, want)
	}
}
