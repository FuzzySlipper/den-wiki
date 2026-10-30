// Package skills renders the den-wiki agent skills for each harness. One
// source per skill lives in skills/source/<skill>/; skills/harnesses.yml lists
// the harnesses. Every file under a skill's source folder is a text/template
// rendered with that harness's values, and the results are committed under
// skills/packages/<harness>/<skill>/ so harnesses can install them straight
// from a checkout.
package skills

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"text/template"

	"gopkg.in/yaml.v3"
)

// Harness is one entry of skills/harnesses.yml. Its fields are the template
// values a skill source can use.
type Harness struct {
	Name      string `yaml:"name"`
	Install   string `yaml:"install"`
	Knowledge string `yaml:"knowledge"`
}

// LoadHarnesses reads <root>/harnesses.yml.
func LoadHarnesses(root string) ([]Harness, error) {
	raw, err := os.ReadFile(filepath.Join(root, "harnesses.yml"))
	if err != nil {
		return nil, err
	}
	var doc struct {
		Harnesses []Harness `yaml:"harnesses"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("harnesses.yml: %w", err)
	}
	seen := map[string]bool{}
	for _, h := range doc.Harnesses {
		if h.Name == "" || h.Install == "" || h.Knowledge == "" {
			return nil, fmt.Errorf("harnesses.yml: harness %q needs name, install and knowledge", h.Name)
		}
		if seen[h.Name] {
			return nil, fmt.Errorf("harnesses.yml: harness %q listed twice", h.Name)
		}
		seen[h.Name] = true
	}
	if len(doc.Harnesses) == 0 {
		return nil, errors.New("harnesses.yml: no harnesses")
	}
	return doc.Harnesses, nil
}

// Render returns every packaged file, keyed by its slash-separated path under
// <root>/packages: <harness>/<skill>/<file>.
func Render(root string) (map[string][]byte, error) {
	harnesses, err := LoadHarnesses(root)
	if err != nil {
		return nil, err
	}
	src := filepath.Join(root, "source")
	var files []string
	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no skill sources under %s", src)
	}
	sort.Strings(files)
	out := map[string][]byte{}
	for _, rel := range files {
		raw, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		tmpl, err := template.New(rel).Option("missingkey=error").Parse(string(raw))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
		for _, h := range harnesses {
			var buf bytes.Buffer
			if err := tmpl.Execute(&buf, h); err != nil {
				return nil, fmt.Errorf("%s for %s: %w", rel, h.Name, err)
			}
			out[h.Name+"/"+rel] = buf.Bytes()
		}
	}
	return out, nil
}

// Sync rewrites <root>/packages to match Render, removing files no source
// produces.
func Sync(root string) error {
	want, err := Render(root)
	if err != nil {
		return err
	}
	pkg := filepath.Join(root, "packages")
	if err := os.RemoveAll(pkg); err != nil {
		return err
	}
	for rel, body := range want {
		path := filepath.Join(pkg, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Drift lists the differences between a fresh render and <root>/packages:
// files that differ, are missing, or are left over. Empty means in sync.
func Drift(root string) ([]string, error) {
	want, err := Render(root)
	if err != nil {
		return nil, err
	}
	pkg := filepath.Join(root, "packages")
	have := map[string][]byte{}
	err = filepath.WalkDir(pkg, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(pkg, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		have[filepath.ToSlash(rel)] = body
		return err
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	var drift []string
	for rel, body := range want {
		got, ok := have[rel]
		switch {
		case !ok:
			drift = append(drift, "missing "+rel)
		case !bytes.Equal(got, body):
			drift = append(drift, "differs "+rel)
		}
	}
	for rel := range have {
		if _, ok := want[rel]; !ok {
			drift = append(drift, "extra "+rel)
		}
	}
	sort.Strings(drift)
	return drift, nil
}
