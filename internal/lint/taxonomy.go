package lint

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// taxonomy is the parsed taxonomy.yml: the allowed page types, tags and
// topics.
type taxonomy struct {
	types  map[string]bool
	tags   map[string]bool
	topics map[string]bool
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
	t := &taxonomy{types: l.taxonomyNames(fields, "types"), tags: l.taxonomyNames(fields, "tags"), topics: l.taxonomyNames(fields, "topics")}
	tags, _ := fields["tags"].([]any)
	for _, it := range tags {
		m, _ := it.(map[string]any)
		name, _ := stringField(m, "name")
		if parent, ok := stringField(m, "parent"); ok && !t.tags[parent] {
			l.add(newFinding(codeTaxonomyParent, taxonomyFile, "tag "+quote(name)+" names parent "+quote(parent)+", which is not a tag"))
		}
	}
	l.checkTopicParents(fields, t.topics)
	return t
}

// checkTopicParents reports topic parents that are not topics, and cycles,
// which would leave a topic with no place in the tree.
func (l *linter) checkTopicParents(fields map[string]any, topics map[string]bool) {
	parents := map[string]string{}
	var names []string
	items, _ := fields["topics"].([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		name, ok := stringField(m, "name")
		parent, hasParent := stringField(m, "parent")
		if !ok || !hasParent {
			continue
		}
		if !topics[parent] {
			l.add(newFinding(codeTopicParent, taxonomyFile, "topic "+quote(name)+" names parent "+quote(parent)+", which is not a topic"))
			continue
		}
		if _, dup := parents[name]; !dup {
			names = append(names, name)
		}
		parents[name] = parent
	}
	for _, name := range names {
		seen := map[string]bool{name: true}
		for p, ok := parents[name]; ok; p, ok = parents[p] {
			if seen[p] {
				if p == name {
					l.add(newFinding(codeTopicParent, taxonomyFile, "topic "+quote(name)+" is its own ancestor; topic parents form a cycle"))
				}
				break
			}
			seen[p] = true
		}
	}
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
