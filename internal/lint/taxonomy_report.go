package lint

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// TaxonomyEntry is one type or tag from taxonomy.yml, with the wiki pages
// that use it.
type TaxonomyEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Parent      string   `json:"parent,omitempty"`
	Pages       []string `json:"pages"`
}

// UnlistedName is a type or tag that pages use but taxonomy.yml does not list.
type UnlistedName struct {
	Name  string   `json:"name"`
	Pages []string `json:"pages"`
}

// TaxonomyReport is the content repo's taxonomy as agents read it: what
// taxonomy.yml allows, what pages use outside it, and the taxonomy.* lint
// findings.
type TaxonomyReport struct {
	Present       bool            `json:"present"`
	Version       int             `json:"version,omitempty"`
	Types         []TaxonomyEntry `json:"types"`
	Tags          []TaxonomyEntry `json:"tags"`
	UnlistedTypes []UnlistedName  `json:"unlisted_types"`
	UnlistedTags  []UnlistedName  `json:"unlisted_tags"`
	Findings      []Finding       `json:"findings"`
}

// ReadTaxonomy parses taxonomy.yml under root and counts its use across the
// wiki pages. A missing or invalid taxonomy is not an error: Present is false
// or Findings says what is wrong, as lint would.
func ReadTaxonomy(root string) (TaxonomyReport, error) {
	r := TaxonomyReport{Types: []TaxonomyEntry{}, Tags: []TaxonomyEntry{}, UnlistedTypes: []UnlistedName{}, UnlistedTags: []UnlistedName{}, Findings: []Finding{}}
	all, err := Lint(root, Options{})
	if err != nil {
		return r, err
	}
	for _, f := range all {
		if strings.HasPrefix(f.Code, "taxonomy.") {
			r.Findings = append(r.Findings, f)
		}
	}
	var fields map[string]any
	src, err := os.ReadFile(filepath.Join(root, taxonomyFile))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return r, err
	default:
		// A file that does not decode is already a taxonomy.invalid finding.
		if fields, err = decodeFrontmatter(src); err == nil {
			r.Present = true
		}
	}
	if v, ok := fields["version"].(json.Number); ok {
		if n, err := v.Int64(); err == nil {
			r.Version = int(n)
		}
	}
	pages, err := loadZone(root, zoneWiki)
	if err != nil {
		return r, err
	}
	typeUse, tagUse := map[string][]string{}, map[string][]string{}
	for _, p := range pages {
		if t, ok := stringField(p.fields, "type"); ok {
			typeUse[t] = append(typeUse[t], p.rel)
		}
		for _, tag := range stringList(p.fields, "tags") {
			tagUse[tag] = append(tagUse[tag], p.rel)
		}
	}
	r.Types = taxonomyEntries(fields, "types", typeUse)
	r.Tags = taxonomyEntries(fields, "tags", tagUse)
	r.UnlistedTypes = unlisted(r.Types, typeUse)
	r.UnlistedTags = unlisted(r.Tags, tagUse)
	return r, nil
}

func taxonomyEntries(fields map[string]any, key string, use map[string][]string) []TaxonomyEntry {
	out := []TaxonomyEntry{}
	seen := map[string]bool{}
	items, _ := fields[key].([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		name, ok := stringField(m, "name")
		if !ok || seen[name] {
			continue
		}
		seen[name] = true
		e := TaxonomyEntry{Name: name, Pages: sortedPaths(use[name])}
		e.Description, _ = stringField(m, "description")
		e.Parent, _ = stringField(m, "parent")
		out = append(out, e)
	}
	return out
}

func unlisted(listed []TaxonomyEntry, use map[string][]string) []UnlistedName {
	known := map[string]bool{}
	for _, e := range listed {
		known[e.Name] = true
	}
	out := []UnlistedName{}
	for name, paths := range use {
		if !known[name] {
			out = append(out, UnlistedName{Name: name, Pages: sortedPaths(paths)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortedPaths(paths []string) []string {
	out := append([]string{}, paths...)
	sort.Strings(out)
	return out
}
