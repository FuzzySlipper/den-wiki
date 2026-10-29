package lint

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// taxonomy is the parsed taxonomy.yml: the allowed page types and tags.
type taxonomy struct {
	types map[string]bool
	tags  map[string]bool
}

// loadTaxonomy reads taxonomy.yml and reports its own findings. It returns nil
// when the file is missing or unreadable, which turns off tag and type checks.
func (l *linter) loadTaxonomy() *taxonomy {
	src, err := os.ReadFile(filepath.Join(l.root, taxonomyFile))
	if errors.Is(err, fs.ErrNotExist) {
		l.add(newFinding(codeTaxonomyMissing, taxonomyFile, "create taxonomy.yml from the content template; tag and type checks are off until it exists"))
		return nil
	}
	if err != nil {
		l.add(newFinding(codeTaxonomyInvalid, taxonomyFile, err.Error()))
		return nil
	}
	fields, err := decodeFrontmatter(src)
	if err != nil {
		l.add(newFinding(codeTaxonomyInvalid, taxonomyFile, err.Error()))
		return nil
	}
	errs, err := validate(l.schemas.taxonomy, fields)
	if err != nil {
		l.add(newFinding(codeTaxonomyInvalid, taxonomyFile, err.Error()))
		return nil
	}
	for _, e := range errs {
		l.add(newFinding(codeTaxonomyInvalid, taxonomyFile, e))
	}
	t := &taxonomy{types: l.taxonomyNames(fields, "types"), tags: l.taxonomyNames(fields, "tags")}
	tags, _ := fields["tags"].([]any)
	for _, it := range tags {
		m, _ := it.(map[string]any)
		name, _ := stringField(m, "name")
		if parent, ok := stringField(m, "parent"); ok && !t.tags[parent] {
			l.add(newFinding(codeTaxonomyParent, taxonomyFile, "tag "+quote(name)+" names parent "+quote(parent)+", which is not a tag"))
		}
	}
	return t
}

func (l *linter) taxonomyNames(fields map[string]any, key string) map[string]bool {
	names := map[string]bool{}
	items, _ := fields[key].([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		name, ok := stringField(m, "name")
		if !ok {
			continue
		}
		if names[name] {
			l.add(newFinding(codeTaxonomyDuplicate, taxonomyFile, key+" lists "+quote(name)+" more than once"))
		}
		names[name] = true
	}
	return names
}
