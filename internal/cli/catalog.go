// Package cli is the den-wiki command line for every registered operation.
// Its catalog (catalog.json) is generated from the operation registry; a
// parity test fails when the two drift, so anything the MCP can do, the CLI
// can do.
package cli

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/FuzzySlipper/den-wiki/internal/registry"
)

// Entry is one operation in the CLI catalog.
type Entry struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Risk        registry.Risk      `json:"risk"`
	Tier        registry.Tier      `json:"tier"`
	Backend     string             `json:"backend"`
	NeedsAgent  bool               `json:"needs_agent"`
	Aliases     []string           `json:"aliases,omitempty"`
	InputSchema map[string]any     `json:"input_schema"`
	Examples    []registry.Example `json:"examples,omitempty"`
}

// Catalog is the generated operation inventory.
type Catalog struct {
	Revision   string  `json:"revision"`
	Operations []Entry `json:"operations"`
}

//go:embed catalog.json
var embedded []byte

// Embedded returns the catalog built into this binary.
func Embedded() (Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(embedded, &c); err != nil {
		return Catalog{}, fmt.Errorf("embedded catalog: %w", err)
	}
	return c, nil
}

// Generate builds the catalog from the registry: every operation, long tail
// included.
func Generate(reg *registry.Registry) Catalog {
	c := Catalog{Revision: reg.Revision()}
	for _, op := range reg.All() {
		c.Operations = append(c.Operations, Entry{
			Name: op.Name, Description: op.Description, Risk: op.Risk, Tier: op.Tier, Backend: op.Backend,
			NeedsAgent: op.NeedsAgent, Aliases: op.Aliases, InputSchema: op.InputSchema(), Examples: op.Examples,
		})
	}
	return c
}

// Marshal renders a catalog in the checked-in form.
func (c Catalog) Marshal() []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(c)
	return b.Bytes()
}

// Find returns the entry for a name or alias.
func (c Catalog) Find(name string) (Entry, bool) {
	for _, e := range c.Operations {
		if e.Name == name {
			return e, true
		}
		for _, a := range e.Aliases {
			if a == name {
				return e, true
			}
		}
	}
	return Entry{}, false
}

// Names returns every operation name, sorted.
func (c Catalog) Names() []string {
	out := make([]string, 0, len(c.Operations))
	for _, e := range c.Operations {
		out = append(out, e.Name)
	}
	sort.Strings(out)
	return out
}

// Diff lists every difference between two catalogs: operations on one side
// only, and operations whose definition differs. Empty means parity.
func Diff(have, want Catalog) []string {
	idx := func(c Catalog) map[string]Entry {
		m := map[string]Entry{}
		for _, e := range c.Operations {
			m[e.Name] = e
		}
		return m
	}
	h, w := idx(have), idx(want)
	var out []string
	for _, n := range want.Names() {
		he, ok := h[n]
		if !ok {
			out = append(out, "missing operation "+n)
			continue
		}
		a, _ := json.Marshal(he)
		b, _ := json.Marshal(w[n])
		if !bytes.Equal(a, b) {
			out = append(out, "operation "+n+" differs")
		}
	}
	for _, n := range have.Names() {
		if _, ok := w[n]; !ok {
			out = append(out, "extra operation "+n)
		}
	}
	if have.Revision != want.Revision {
		out = append(out, fmt.Sprintf("revision %s != %s", have.Revision, want.Revision))
	}
	return out
}
