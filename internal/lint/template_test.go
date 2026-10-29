package lint

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/FuzzySlipper/den-wiki/schemas"
)

const contentTemplate = "../../templates/content"

// The OpenKnowledge config in the content template points at copies of the
// schemas; they must not drift from the source of truth.
func TestTemplateSchemaCopiesMatch(t *testing.T) {
	names, err := filepath.Glob("../../schemas/*.schema.json")
	if err != nil || len(names) == 0 {
		t.Fatalf("no schemas found: %v", err)
	}
	for _, src := range names {
		want, err := schemas.Files.ReadFile(filepath.Base(src))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(contentTemplate, ".ok/schemas", filepath.Base(src)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("templates/content/.ok/schemas/%s differs from schemas/; copy it again", filepath.Base(src))
		}
	}
}

// The per-folder OpenKnowledge templates are what agents start new files from,
// so each must satisfy its zone's schema and rules.
func TestFolderTemplatesFollowContract(t *testing.T) {
	sch, err := compileSchemas()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		rel  string
		zone string
	}{
		{"wiki/.ok/templates/page.md", zoneWiki},
		{"sources/.ok/templates/source.md", zoneSources},
		{"trails/.ok/templates/trail.md", zoneTrails},
	}
	for _, c := range cases {
		t.Run(c.rel, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join(contentTemplate, c.rel))
			if err != nil {
				t.Fatal(err)
			}
			doc, err := parseDocument(src)
			if err != nil || !doc.hasFrontmatter {
				t.Fatalf("template has no valid frontmatter: %v", err)
			}
			if shape := zoneShape(doc.fields); shape != c.zone {
				t.Fatalf("template frontmatter is shaped for %q", shape)
			}
			zoneSchema := map[string]*jsonschema.Schema{zoneWiki: sch.page, zoneSources: sch.source, zoneTrails: sch.trail}[c.zone]
			errs, err := validate(zoneSchema, doc.fields)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range errs {
				t.Errorf("schema: %s", e)
			}
			if c.zone == zoneTrails {
				l := &linter{}
				l.checkTrail(c.rel, doc)
				for _, f := range l.out {
					t.Errorf("%s", f)
				}
			}
		})
	}
}
