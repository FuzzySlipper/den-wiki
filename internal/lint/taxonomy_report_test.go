package lint

import (
	"reflect"
	"testing"
)

func TestReadTaxonomy(t *testing.T) {
	root := t.TempDir()
	writeContent(t, root, map[string]string{
		"taxonomy.yml": `version: 1
types:
  - {name: concept, description: An idea.}
  - {name: hub, description: A map page.}
tags:
  - {name: software, description: Software.}
  - {name: ai, description: AI., parent: software}
  - {name: games, description: Games.}
`,
		"wiki/alpha.md": curPage("Alpha", "", "a"),
		"wiki/beta.md":  "---\ntype: essay\ntitle: Beta\ntags: [software, databases]\ncuration: unreviewed\nupdated: 2026-10-01\n---\n\nb\n",
	})
	r, err := ReadTaxonomy(root)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Present || r.Version != 1 {
		t.Fatalf("present %v version %d", r.Present, r.Version)
	}
	want := []TaxonomyEntry{
		{Name: "software", Description: "Software.", Pages: []string{"wiki/alpha.md", "wiki/beta.md"}},
		{Name: "ai", Description: "AI.", Parent: "software", Pages: []string{}},
		{Name: "games", Description: "Games.", Pages: []string{}},
	}
	if !reflect.DeepEqual(r.Tags, want) {
		t.Errorf("tags %+v", r.Tags)
	}
	if len(r.Types) != 2 || r.Types[0].Name != "concept" || !reflect.DeepEqual(r.Types[0].Pages, []string{"wiki/alpha.md"}) {
		t.Errorf("types %+v", r.Types)
	}
	if !reflect.DeepEqual(r.UnlistedTags, []UnlistedName{{Name: "databases", Pages: []string{"wiki/beta.md"}}}) {
		t.Errorf("unlisted tags %+v", r.UnlistedTags)
	}
	if !reflect.DeepEqual(r.UnlistedTypes, []UnlistedName{{Name: "essay", Pages: []string{"wiki/beta.md"}}}) {
		t.Errorf("unlisted types %+v", r.UnlistedTypes)
	}
	codes := map[string]bool{}
	for _, f := range r.Findings {
		codes[f.Code] = true
		if f.Code[:9] != "taxonomy." {
			t.Errorf("non-taxonomy finding %s", f.Code)
		}
	}
	if !codes["taxonomy.unknown_tag"] || !codes["taxonomy.unknown_type"] {
		t.Errorf("findings %+v", r.Findings)
	}
}

func TestReadTaxonomyMissing(t *testing.T) {
	root := t.TempDir()
	writeContent(t, root, map[string]string{"wiki/alpha.md": curPage("Alpha", "", "a")})
	r, err := ReadTaxonomy(root)
	if err != nil {
		t.Fatal(err)
	}
	if r.Present || len(r.Tags) != 0 || len(r.Findings) != 1 || r.Findings[0].Code != "taxonomy.missing" {
		t.Fatalf("%+v", r)
	}
	if len(r.UnlistedTags) != 1 || r.UnlistedTags[0].Name != "software" {
		t.Errorf("unlisted %+v", r.UnlistedTags)
	}
}

func TestReadTaxonomyTopics(t *testing.T) {
	root := t.TempDir()
	writeContent(t, root, map[string]string{
		"taxonomy.yml": `version: 1
types:
  - {name: concept, description: An idea.}
tags:
  - {name: software, description: Software.}
topics:
  - {name: dev, description: Development.}
  - {name: gamedev, description: Games., parent: dev}
`,
		"wiki/alpha.md": curPage("Alpha", "topic: gamedev\n", "a"),
		"wiki/beta.md":  curPage("Beta", "topic: cooking\n", "b"),
		"wiki/gamma.md": curPage("Gamma", "", "c"),
	})
	r, err := ReadTaxonomy(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []TaxonomyEntry{
		{Name: "dev", Description: "Development.", Pages: []string{}},
		{Name: "gamedev", Description: "Games.", Parent: "dev", Pages: []string{"wiki/alpha.md"}},
	}
	if !reflect.DeepEqual(r.Topics, want) {
		t.Errorf("topics %+v", r.Topics)
	}
	if !reflect.DeepEqual(r.UnlistedTopics, []UnlistedName{{Name: "cooking", Pages: []string{"wiki/beta.md"}}}) {
		t.Errorf("unlisted topics %+v", r.UnlistedTopics)
	}
	if !reflect.DeepEqual(r.Unsorted, []string{"wiki/gamma.md"}) {
		t.Errorf("unsorted %+v", r.Unsorted)
	}
	codes := map[string]bool{}
	for _, f := range r.Findings {
		codes[f.Code] = true
	}
	if !codes["taxonomy.unknown_topic"] {
		t.Errorf("findings %+v", r.Findings)
	}
}
