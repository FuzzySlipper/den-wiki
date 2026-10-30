# den-wiki

Contracts and tooling for the den-wiki research knowledge base. The content
itself lives in a separate repository, `den-wiki-content`, served by
OpenKnowledge.

- [`docs/content-contract.md`](docs/content-contract.md): zones, naming,
  frontmatter, citations, the `(trail:<slug>)` commit tag and the trail format.
- [`schemas/`](schemas): JSON Schemas for `wiki/` pages, `sources/`, `trails/`
  and `taxonomy.yml`.
- [`templates/content/`](templates/content): the skeleton a new content
  repository starts from.
- `den-wiki serve`: the MCP facade over OpenKnowledge and the research ledger
  ([facade](docs/facade.md), [ledger](docs/ledger.md)).
- `den-wiki <operation>`: every MCP operation from the command line
  ([CLI](docs/cli.md)).
- `den-wiki lint`: checks a content directory against the contract.
- [`deploy/`](deploy): the den-prox-mini container, units, firewall and Caddy
  site at `https://wiki.dragonden.stream` ([deployment](docs/deployment.md)).
- [`skills/`](skills): the `den-wiki-research` and `den-wiki-lookup` agent
  skills, one source packaged per harness ([skills](docs/skills.md)).

```bash
CGO_ENABLED=0 go build -trimpath -o ~/.local/bin/den-wiki ./cmd/den-wiki
den-wiki serve --config configs/den-wiki.example.yml
den-wiki search memory
den-wiki wiki_search --query "agent memory" --agent me --human
den-wiki lint --format text path/to/den-wiki-content
```

Verify with `scripts/verify.sh`; run the OpenKnowledge integration tests with
`scripts/ok-integration.sh <dir> --isolate-home`.
