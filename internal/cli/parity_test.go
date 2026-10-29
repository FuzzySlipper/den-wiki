package cli

import (
	"strings"
	"testing"

	"github.com/FuzzySlipper/den-wiki/internal/facade"
)

func generated(t *testing.T) Catalog {
	t.Helper()
	reg, err := facade.NewRegistry(facade.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	return Generate(reg)
}

// The CLI catalog must list exactly the registry's operations, long tail
// included, with identical definitions. There is no allowlist.
func TestCatalogParity(t *testing.T) {
	emb, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if d := Diff(emb, generated(t)); len(d) > 0 {
		t.Fatalf("internal/cli/catalog.json is out of date; run go generate ./internal/cli\n  %s", strings.Join(d, "\n  "))
	}
}

func dropOp(c Catalog, name string) Catalog {
	out := Catalog{Revision: c.Revision}
	for _, e := range c.Operations {
		if e.Name != name {
			out.Operations = append(out.Operations, e)
		}
	}
	return out
}

// The parity check fails when an operation is removed from either side.
func TestParityCheckCatchesDrift(t *testing.T) {
	gen := generated(t)
	if len(gen.Operations) == 0 {
		t.Fatal("registry has no operations")
	}
	victim := gen.Operations[len(gen.Operations)/2].Name
	if d := Diff(dropOp(gen, victim), gen); len(d) != 1 || d[0] != "missing operation "+victim {
		t.Errorf("removed from CLI side: diff %v", d)
	}
	if d := Diff(gen, dropOp(gen, victim)); len(d) != 1 || d[0] != "extra operation "+victim {
		t.Errorf("removed from registry side: diff %v", d)
	}
	changed := generated(t)
	changed.Operations[0].Description += " (edited)"
	if d := Diff(changed, gen); len(d) != 1 || !strings.Contains(d[0], "differs") {
		t.Errorf("changed definition: diff %v", d)
	}
}
