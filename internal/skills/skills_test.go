package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const repoSkills = "../../skills"

// TestPackagesMatchSource is the drift test: the committed packages must be
// exactly what the sources render to. Fix with go run ./cmd/den-wiki-skills.
func TestPackagesMatchSource(t *testing.T) {
	drift, err := Drift(repoSkills)
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) > 0 {
		t.Fatalf("skills/packages is out of date; run go run ./cmd/den-wiki-skills:\n%s", strings.Join(drift, "\n"))
	}
}

func TestEveryHarnessGetsEverySkill(t *testing.T) {
	out, err := Render(repoSkills)
	if err != nil {
		t.Fatal(err)
	}
	harnesses, err := LoadHarnesses(repoSkills)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range harnesses {
		for _, skill := range []string{"den-wiki-research", "den-wiki-lookup"} {
			body, ok := out[h.Name+"/"+skill+"/SKILL.md"]
			if !ok {
				t.Fatalf("%s has no %s", h.Name, skill)
			}
			if !strings.HasPrefix(string(body), "---\nname: "+skill+"\n") {
				t.Errorf("%s/%s: SKILL.md must open with frontmatter naming the skill", h.Name, skill)
			}
			if !strings.Contains(string(body), "harness `"+h.Name+"`") {
				t.Errorf("%s/%s does not name its harness", h.Name, skill)
			}
		}
	}
}

func TestResearchSkillPointsAtResearchMethods(t *testing.T) {
	out, err := Render(repoSkills)
	if err != nil {
		t.Fatal(err)
	}
	for path, body := range out {
		if strings.HasSuffix(path, "den-wiki-research/SKILL.md") && !strings.Contains(string(body), "`research-methods`") {
			t.Errorf("%s does not send agents to the research-methods Knowledge entry", path)
		}
	}
}

func TestDriftReportsChangedMissingAndExtraFiles(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("harnesses.yml", "harnesses:\n  - {name: a, install: ~/a, knowledge: k}\n  - {name: b, install: ~/b, knowledge: k}\n")
	write("source/s/SKILL.md", "harness {{.Name}}\n")
	if err := Sync(root); err != nil {
		t.Fatal(err)
	}
	if drift, err := Drift(root); err != nil || len(drift) != 0 {
		t.Fatalf("fresh sync drifted: %v %v", drift, err)
	}
	write("packages/a/s/SKILL.md", "edited by hand\n")
	write("packages/c/s/SKILL.md", "stale harness\n")
	if err := os.Remove(filepath.Join(root, "packages/b/s/SKILL.md")); err != nil {
		t.Fatal(err)
	}
	drift, err := Drift(root)
	if err != nil {
		t.Fatal(err)
	}
	want := "differs a/s/SKILL.md,extra c/s/SKILL.md,missing b/s/SKILL.md"
	if got := strings.Join(drift, ","); got != want {
		t.Fatalf("drift = %s, want %s", got, want)
	}
}

func TestUnknownTemplateFieldFailsTheRender(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "source/s"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "harnesses.yml"), []byte("harnesses:\n  - {name: a, install: ~/a, knowledge: k}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source/s/SKILL.md"), []byte("{{.Nope}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Render(root); err == nil {
		t.Fatal("rendering an unknown field succeeded")
	}
}
