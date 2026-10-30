# Agent skills

den-wiki ships three skills that teach agents in every harness to use the wiki
the same way, through the den-wiki MCP or CLI only:

| Skill | For |
|---|---|
| `den-wiki-research` | A research run: check prior coverage, check the `research-methods` Knowledge entry, claim a ledger job, write the trail, capture sources, write pages with citations, lint and finish |
| `den-wiki-lookup` | Read-only lookups that do not research or write |
| `den-wiki-librarian` | A curation pass: scan, act on duplicates, names, links, hubs, proposals, staleness, size, sources and the unreviewed backlog, and record it all in a curation trail ([curation](curation.md)) |

The skills teach the den-wiki mechanics. How to investigate a given kind of
question lives in Den Knowledge: the entry `research-methods` indexes method
entries with the kinds of questions each applies to, and the research skill
sends agents there. Methods change without a den-wiki release, and an agent
reads only the ones that fit its question.

## Layout

```
skills/
  source/<skill>/SKILL.md     the one source of each skill (text/template)
  harnesses.yml               the harnesses: name, install folder, how to read Knowledge
  packages/<harness>/<skill>/ generated; committed so harnesses install from a checkout
```

A source may use each harness's fields: `{{.Name}}` (the value agents pass as
`harness`), `{{.Install}}` and `{{.Knowledge}}`. After changing a source or
`harnesses.yml`, regenerate the packages:

```bash
go run ./cmd/den-wiki-skills
```

The drift test (`internal/skills`, part of `go test ./...` and so of `verify`)
fails when a committed package differs from a fresh render, or a package is
missing or left over.

## Install

```bash
scripts/install-skills.sh <harness>
```

links each skill in `skills/packages/<harness>/` into the harness's skills
folder, so a `git pull` updates it. `--dest <dir>` overrides the folder and
`--copy` copies instead of linking, for a user who cannot read the checkout.

## MCP registration

Each harness reaches the facade at `https://wiki.dragonden.stream/mcp` and
names its agent in the `X-Den-Wiki-Agent` header (writes need it). On
den-agents:

| Harness | Config | Agent identity |
|---|---|---|
| Claude Code | `~/.claude.json` user scope (`claude mcp add --scope user --transport http den-wiki <url> --header 'X-Den-Wiki-Agent: claude-code-den-agents'`) | `claude-code-den-agents` |
| Codex | `~/.codex/config.toml`: `[mcp_servers.den-wiki]` `url`, `[mcp_servers.den-wiki.http_headers]` | `codex-den-agents` |
| DSH | `~/.dsh/profiles/web/cordis.patch.yml`: an `mcp-den-wiki` `@deepseek-ai/dsh-mcp-client` insert, `transport: streamable-http`, with `headers` | `dsh-den-agents` |
| Hermes | the owner's Hermes profile `kate` on den-k8 (user `hermes-mj`): `mcp_servers.den-wiki` with `url` and `headers` in its `config.yaml` | `hermes-kate` |
