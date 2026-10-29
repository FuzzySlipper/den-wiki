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
- `den-wiki lint`: checks a content directory against the contract.

```bash
go run ./cmd/den-wiki lint --format text path/to/den-wiki-content
go run ./cmd/den-wiki lint --commits origin/main..HEAD --fail-on error path/to/den-wiki-content
go run ./cmd/den-wiki lint --list-rules --format text
```

Verify with `scripts/verify.sh`.
