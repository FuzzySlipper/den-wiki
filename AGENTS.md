# den-wiki

den-wiki owns the contracts and thin glue around a research knowledge base: the
content contract and its lint, and later the research ledger, the `den-wiki`
MCP facade with a parity CLI, curation runs, agent skills and deployment. It
must not become a wiki UI, a Markdown server, a link graph or a search engine;
those come from OpenKnowledge, run unmodified as a pinned npm package and
reached only over HTTP/MCP (it is GPL-3; den-wiki's code stays outside that
licence).

Den guidance: run `den-tool den get_agent_guidance --project-id _global` and read `_global/den-operating-contract` before task work. Project documents: `den-wiki/den-wiki-design`, `den-wiki/ok-integration-findings`, `den-wiki/known-limitations`.

Repository rules:
- `docs/content-contract.md` is the current-state content contract and `schemas/` is the source of truth for frontmatter. A change to either lands with the matching lint change and tests in the same commit; if it changes the design, update `den-wiki/den-wiki-design` in the same task.
- Lint rules are findings, never write blocks. Each has a stable code registered in `internal/lint/rules.go`, listed in the contract doc and covered by a failing fixture case; tests enforce all three. Never rename or reuse a code.
- `templates/content/` is the skeleton for `den-wiki-content` and must lint clean. Its `.ok/schemas/` are byte-identical copies of `schemas/`; copy them again when a schema changes.
- The content repository is not this repository. Do not add research content here beyond test fixtures.
- One Go module, standard library first. Keep the CLI thin and put rule logic in `internal/`.
- One operation registry (`internal/registry`) is the source for MCP tools and the CLI. Operations are added in `internal/facade`; the facade owns no domain state. See `docs/facade.md`.
- OpenKnowledge is reached only over its MCP endpoint, pinned in `scripts/ok-version`. Never vendor, link or patch it.
- Skills are edited only in `skills/source/` and `skills/harnesses.yml`; regenerate `skills/packages/` with `go run ./cmd/den-wiki-skills` (the drift test fails otherwise). Research method guidance belongs in the Den Knowledge entry `research-methods` and the entries it lists, not in the skills. See `docs/skills.md`.
- Configuration comes from flags and YAML files, never environment variables.
- Before review: `scripts/verify.sh` (gofumpt, `go vet ./...`, `go test ./...`, template lint), and `scripts/ok-integration.sh <dir> --isolate-home` when the facade or content template changes. CI runs them as the `verify` and `ok-integration` jobs.
