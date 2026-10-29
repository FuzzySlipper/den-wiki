// Package schemas embeds the JSON Schemas of the den-wiki content contract.
// The files in this directory are the source of truth; docs/content-contract.md
// describes them and lint validates frontmatter against them.
package schemas

import "embed"

// Files holds every *.schema.json file in this directory.
//
//go:embed *.schema.json
var Files embed.FS

// Schema file names, one per contract surface.
const (
	WikiPage = "wiki-page.schema.json"
	Source   = "source.schema.json"
	Trail    = "trail.schema.json"
	Taxonomy = "taxonomy.schema.json"
)
