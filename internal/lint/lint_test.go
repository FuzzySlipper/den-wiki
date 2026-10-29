package lint

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validFixture = "testdata/valid"

// copyTree copies a fixture into a fresh temporary directory.
func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func replace(t *testing.T, root, rel, old, new string) {
	t.Helper()
	s := read(t, root, rel)
	if !strings.Contains(s, old) {
		t.Fatalf("%s does not contain %q", rel, old)
	}
	write(t, root, rel, strings.Replace(s, old, new, 1))
}

func remove(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
		t.Fatal(err)
	}
}

func mustLint(t *testing.T, root string, opts Options) []Finding {
	t.Helper()
	fs, err := Lint(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func hasFinding(fs []Finding, code, path string) bool {
	for _, f := range fs {
		if f.Code == code && f.Path == path {
			return true
		}
	}
	return false
}

func dump(fs []Finding) string {
	var b strings.Builder
	for _, f := range fs {
		b.WriteString("\n  " + f.String())
	}
	return b.String()
}

func TestValidFixturePasses(t *testing.T) {
	if fs := mustLint(t, validFixture, Options{}); len(fs) != 0 {
		t.Fatalf("valid fixture has findings:%s", dump(fs))
	}
}

func TestContentTemplatePasses(t *testing.T) {
	if fs := mustLint(t, "../../templates/content", Options{}); len(fs) != 0 {
		t.Fatalf("content template has findings:%s", dump(fs))
	}
}

const (
	page   = "wiki/agent-memory.md"
	trail  = "trails/agent-memory-research-2026-10-28.md"
	source = "sources/agent-memory-survey.md"
)

// Each case breaks one rule in a copy of the valid fixture and expects that
// rule's code at that path.
func TestEachRuleFails(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, root string)
		code   string
		path   string
	}{
		{"file at root", func(t *testing.T, r string) { write(t, r, "notes.md", "# notes\n") }, "zone.outside", "notes.md"},
		{"folder at root", func(t *testing.T, r string) { write(t, r, "drafts/a.md", "# a\n") }, "zone.outside", "drafts"},
		{"zone missing", func(t *testing.T, r string) { remove(t, r, "trails") }, "zone.missing", "trails"},
		{"zone rules missing", func(t *testing.T, r string) { remove(t, r, "wiki/.ok") }, "zone.rules_missing", "wiki"},
		{"trail in wiki", func(t *testing.T, r string) { write(t, r, "wiki/run.md", read(t, r, trail)) }, "zone.misplaced", "wiki/run.md"},
		{"source in trails", func(t *testing.T, r string) { write(t, r, "trails/copy.md", read(t, r, source)) }, "zone.misplaced", "trails/copy.md"},
		{"page in sources", func(t *testing.T, r string) { write(t, r, "sources/page.md", read(t, r, page)) }, "zone.misplaced", "sources/page.md"},
		{"non-markdown", func(t *testing.T, r string) { write(t, r, "sources/paper.pdf", "%PDF") }, "zone.non_markdown", "sources/paper.pdf"},
		{"nested trail", func(t *testing.T, r string) { write(t, r, "trails/2026/run.md", read(t, r, trail)) }, "zone.nested_trail", "trails/2026/run.md"},
		{"not a slug", func(t *testing.T, r string) { write(t, r, "wiki/Agent_Memory.md", read(t, r, page)) }, "naming.slug", "wiki/Agent_Memory.md"},
		{"folder not a slug", func(t *testing.T, r string) { write(t, r, "wiki/My Notes/x.md", read(t, r, page)) }, "naming.slug", "wiki/My Notes"},
		{"dated name", func(t *testing.T, r string) { write(t, r, "wiki/memory-2026-10-29.md", read(t, r, page)) }, "naming.date", "wiki/memory-2026-10-29.md"},
		{"compact date", func(t *testing.T, r string) { write(t, r, "wiki/memory-20261029.md", read(t, r, page)) }, "naming.date", "wiki/memory-20261029.md"},
		{"dated folder", func(t *testing.T, r string) { write(t, r, "wiki/2026-10/memory.md", read(t, r, page)) }, "naming.date", "wiki/2026-10"},
		{"sha name", func(t *testing.T, r string) { write(t, r, "wiki/memory-3f9a2c1.md", read(t, r, page)) }, "naming.sha", "wiki/memory-3f9a2c1.md"},
		{"v2 name", func(t *testing.T, r string) { write(t, r, "wiki/memory-v2.md", read(t, r, page)) }, "naming.version_marker", "wiki/memory-v2.md"},
		{"final name", func(t *testing.T, r string) { write(t, r, "wiki/memory-final.md", read(t, r, page)) }, "naming.version_marker", "wiki/memory-final.md"},
		{"new name", func(t *testing.T, r string) { write(t, r, "wiki/memory-new.md", read(t, r, page)) }, "naming.version_marker", "wiki/memory-new.md"},
		{"updated name", func(t *testing.T, r string) { write(t, r, "wiki/memory-updated.md", read(t, r, page)) }, "naming.version_marker", "wiki/memory-updated.md"},
		{"old folder", func(t *testing.T, r string) { write(t, r, "wiki/memory-old/x.md", read(t, r, page)) }, "naming.version_marker", "wiki/memory-old"},
		{"no frontmatter", func(t *testing.T, r string) { write(t, r, "wiki/bare.md", "# Bare\n") }, "frontmatter.missing", "wiki/bare.md"},
		{"unclosed frontmatter", func(t *testing.T, r string) { write(t, r, "wiki/open.md", "---\ntitle: x\n") }, "frontmatter.invalid", "wiki/open.md"},
		{"list frontmatter", func(t *testing.T, r string) { write(t, r, "wiki/list.md", "---\n- a\n---\n") }, "frontmatter.invalid", "wiki/list.md"},
		{"bad YAML", func(t *testing.T, r string) { write(t, r, "wiki/bad.md", "---\ntitle: [x\n---\n") }, "frontmatter.invalid", "wiki/bad.md"},
		{"page schema", func(t *testing.T, r string) { replace(t, r, page, "curation: unreviewed", "curation: maybe") }, "frontmatter.schema", page},
		{"page required field", func(t *testing.T, r string) { replace(t, r, page, "updated: 2026-10-29\n", "") }, "frontmatter.schema", page},
		{"source schema", func(t *testing.T, r string) {
			replace(t, r, source, "source_url: https://example.org/papers/agent-memory-survey", "source_url: not a url")
		}, "frontmatter.schema", source},
		{"trail schema", func(t *testing.T, r string) { replace(t, r, trail, "harness: claude-code\n", "") }, "frontmatter.schema", trail},
		{"index frontmatter", func(t *testing.T, r string) { write(t, r, "wiki/index.md", "---\ntitle: Wiki\n---\n") }, "index.frontmatter", "wiki/index.md"},
		{"root index extra key", func(t *testing.T, r string) { replace(t, r, "index.md", "okf_version", "title: x\nokf_version") }, "index.frontmatter", "index.md"},
		{"taxonomy missing", func(t *testing.T, r string) { remove(t, r, "taxonomy.yml") }, "taxonomy.missing", "taxonomy.yml"},
		{"taxonomy schema", func(t *testing.T, r string) { replace(t, r, "taxonomy.yml", "version: 1", "version: 2") }, "taxonomy.invalid", "taxonomy.yml"},
		{"taxonomy not YAML", func(t *testing.T, r string) { write(t, r, "taxonomy.yml", "tags: [\n") }, "taxonomy.invalid", "taxonomy.yml"},
		{"taxonomy duplicate", func(t *testing.T, r string) {
			write(t, r, "taxonomy.yml", read(t, r, "taxonomy.yml")+"  - name: games\n    description: again\n")
		}, "taxonomy.duplicate", "taxonomy.yml"},
		{"taxonomy parent", func(t *testing.T, r string) { replace(t, r, "taxonomy.yml", "parent: software", "parent: nowhere") }, "taxonomy.unknown_parent", "taxonomy.yml"},
		{"unknown tag", func(t *testing.T, r string) { replace(t, r, page, "tags: [ai, research-method]", "tags: [ai, gossip]") }, "taxonomy.unknown_tag", page},
		{"unknown type", func(t *testing.T, r string) { replace(t, r, page, "type: concept", "type: essay") }, "taxonomy.unknown_type", page},
		{"missing source", func(t *testing.T, r string) { remove(t, r, "sources/agent-memory-survey-refetch-2026-10-29.md") }, "ref.missing_source", page},
		{"missing trail on page", func(t *testing.T, r string) {
			replace(t, r, page, "trails: [agent-memory-research-2026-10-28]", "trails: [no-such-run]")
		}, "ref.missing_trail", page},
		{"missing trail on source", func(t *testing.T, r string) { remove(t, r, trail) }, "ref.missing_trail", source},
		{"missing contradicts page", func(t *testing.T, r string) { remove(t, r, "wiki/ai") }, "ref.missing_page", page},
		{"deprecated key", func(t *testing.T, r string) {
			replace(t, r, page, "curation: unreviewed", "curation: unreviewed\ndeprecated: true")
		}, "wiki.relabelled", page},
		{"superseded_by key", func(t *testing.T, r string) {
			replace(t, r, page, "curation: unreviewed", "curation: unreviewed\nsuperseded_by: wiki/other.md")
		}, "wiki.relabelled", page},
		{"deprecated title", func(t *testing.T, r string) {
			replace(t, r, page, "title: Agent memory", "title: \"[DEPRECATED] Agent memory\"")
		}, "wiki.relabelled", page},
		{"alias collision", func(t *testing.T, r string) {
			write(t, r, "wiki/rag.md", strings.Replace(read(t, r, "wiki/ai/retrieval-augmented-generation.md"), "title: Retrieval-augmented generation", "title: RAG pipelines", 1))
		}, "wiki.name_collision", "wiki/rag.md"},
		{"citation to missing source", func(t *testing.T, r string) {
			replace(t, r, page, "(../sources/agent-memory-survey.md)", "(../sources/gone.md)")
		}, "citation.missing_source", page},
		{"citation without quote", func(t *testing.T, r string) {
			replace(t, r, page, ` "Agents that keep an external, editable memory answer follow-up questions more consistently"`, "")
		}, "citation.missing_quote", page},
		{"source edited", func(t *testing.T, r string) { replace(t, r, source, "more\nconsistently", "far more\nconsistently") }, "source.sha256_mismatch", source},
		{"supersedes missing", func(t *testing.T, r string) {
			replace(t, r, "sources/agent-memory-survey-refetch-2026-10-29.md", "supersedes: sources/agent-memory-survey.md", "supersedes: sources/never-captured.md")
		}, "source.supersedes_missing", "sources/agent-memory-survey-refetch-2026-10-29.md"},
		{"supersedes itself", func(t *testing.T, r string) {
			p := "sources/agent-memory-survey-refetch-2026-10-29.md"
			replace(t, r, p, "supersedes: sources/agent-memory-survey.md", "supersedes: "+p)
		}, "source.supersedes_missing", "sources/agent-memory-survey-refetch-2026-10-29.md"},
		{"trail section missing", func(t *testing.T, r string) { replace(t, r, trail, "## Dead ends\n", "") }, "trail.section_missing", trail},
		{"trail section only in code", func(t *testing.T, r string) { replace(t, r, trail, "## Dead ends\n", "```\n## Dead ends\n```\n") }, "trail.section_missing", trail},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := copyTree(t, validFixture)
			c.mutate(t, root)
			fs := mustLint(t, root, Options{})
			if !hasFinding(fs, c.code, c.path) {
				t.Fatalf("want %s at %s, got:%s", c.code, c.path, dump(fs))
			}
		})
	}
}

func TestEveryRuleHasAFailingCase(t *testing.T) {
	src, err := os.ReadFile("lint_test.go")
	if err != nil {
		t.Fatal(err)
	}
	commitSrc, err := os.ReadFile("commits_test.go")
	if err != nil {
		t.Fatal(err)
	}
	all := string(src) + string(commitSrc)
	for _, r := range Rules() {
		if !strings.Contains(all, `"`+r.Code+`"`) {
			t.Errorf("rule %s has no test case", r.Code)
		}
	}
}

func TestRulesAreDocumented(t *testing.T) {
	doc, err := os.ReadFile("../../docs/content-contract.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range Rules() {
		if !strings.Contains(string(doc), "`"+r.Code+"`") {
			t.Errorf("rule %s is not listed in docs/content-contract.md", r.Code)
		}
	}
}

func TestTrailSectionHeadingsTolerateNumbering(t *testing.T) {
	root := copyTree(t, validFixture)
	replace(t, root, trail, "## Dead ends\n", "## 5. Dead ends:\n")
	if fs := mustLint(t, root, Options{}); len(fs) != 0 {
		t.Fatalf("numbered heading should match:%s", dump(fs))
	}
}

func TestSourceHashReportsComputedValue(t *testing.T) {
	root := copyTree(t, validFixture)
	replace(t, root, source, "more\nconsistently", "far more\nconsistently")
	doc, err := parseDocument([]byte(read(t, root, source)))
	if err != nil {
		t.Fatal(err)
	}
	want := bodySHA256(doc.body)
	for _, f := range mustLint(t, root, Options{}) {
		if f.Code == "source.sha256_mismatch" && strings.Contains(f.Message, want) {
			return
		}
	}
	t.Fatalf("finding does not report computed hash %s", want)
}

func TestAssetFoldersAreSkipped(t *testing.T) {
	root := copyTree(t, validFixture)
	write(t, root, "wiki/Agent Memory 2026.assets/Shot_v2.PNG", "x")
	if fs := mustLint(t, root, Options{}); len(fs) != 0 {
		t.Fatalf("asset folder should be skipped:%s", dump(fs))
	}
}

func TestSeverities(t *testing.T) {
	fs := []Finding{newFinding("zone.missing", "x", ""), newFinding("naming.slug", "y", "")}
	if !AtLeast(fs, Warning) || AtLeast(fs, Error) {
		t.Fatalf("AtLeast wrong for %v", fs)
	}
	if c := Counts(fs); c["info"] != 1 || c["warning"] != 1 || c["error"] != 0 {
		t.Fatalf("Counts = %v", c)
	}
	for _, name := range []string{"info", "warning", "error"} {
		s, err := ParseSeverity(name)
		if err != nil || s.String() != name {
			t.Fatalf("ParseSeverity(%q) = %v, %v", name, s, err)
		}
	}
	if _, err := ParseSeverity("fatal"); err == nil {
		t.Fatal("ParseSeverity accepted fatal")
	}
}
