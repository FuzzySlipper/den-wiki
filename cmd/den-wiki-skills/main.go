// Command den-wiki-skills regenerates skills/packages from skills/source and
// skills/harnesses.yml. Run it after changing a skill or a harness; the drift
// test (internal/skills) fails until you do.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/FuzzySlipper/den-wiki/internal/skills"
)

func main() {
	root := flag.String("root", "skills", "the skills folder: source/, harnesses.yml and packages/")
	flag.Parse()
	if err := skills.Sync(*root); err != nil {
		fmt.Fprintln(os.Stderr, "den-wiki-skills:", err)
		os.Exit(1)
	}
}
