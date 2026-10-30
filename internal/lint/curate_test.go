package lint

import (
	"os"
	"path/filepath"
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
		"wiki/agent-memory.md": curPage("Agent memory", "volatility: cold\nsources: [sources/cited.md]\n", "unrelated"),
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
