// Command den-wiki-catalog regenerates internal/cli/catalog.json from the
// operation registry. Run it (or go generate ./internal/cli) after changing
// any operation; the CLI parity test fails until you do.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/FuzzySlipper/den-wiki/internal/cli"
	"github.com/FuzzySlipper/den-wiki/internal/facade"
)

func main() {
	out := flag.String("output", "internal/cli/catalog.json", "where to write the catalog")
	flag.Parse()
	reg, err := facade.NewRegistry(facade.Deps{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "den-wiki-catalog:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, cli.Generate(reg).Marshal(), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "den-wiki-catalog:", err)
		os.Exit(1)
	}
}
