# den-wiki CLI

`den-wiki` is one static Go binary. It is the service (`den-wiki serve`), the
local content linter (`den-wiki lint`), and a client for every operation the
den-wiki MCP endpoint offers, for agents without direct MCP access.

## Install

```bash
CGO_ENABLED=0 go build -trimpath -o ~/.local/bin/den-wiki ./cmd/den-wiki
```

No runtime dependencies. Installing on harness hosts is part of deployment
(#10466).

## Configure

Flags win over the config file, `~/.config/den-wiki/cli.yml` (or `--config`).
There are no environment variables.

```yaml
endpoint: http://127.0.0.1:7341/mcp   # the den-wiki MCP endpoint
agent: my-agent-name                   # sent with every call; writes need it
```

## Discover

```bash
den-wiki help                      # every operation, one line each
den-wiki search lease              # operations matching all terms
den-wiki describe job_claim        # full entry with input schema (JSON)
den-wiki job_claim --help          # fields, limits, examples, schema
den-wiki catalog                   # the whole catalog as JSON
den-wiki health                    # /healthz beside the endpoint; exit 6 unless all ok
```

## Call

Fields are flags named after the schema fields; `--lease_token` and
`--lease-token` are the same. Arrays take `a,b,c`, a JSON array, or the flag
repeated; objects take JSON. Or pass one JSON object with `--args-json`
(`@file`, `@-` for stdin), which cannot be mixed with field flags. A
`*_base64` field given `@<path>`, as a flag or in `--args-json`, sends that
local file base64 encoded (base64 has no `@`, so a real value is never taken
for a path).

```bash
den-wiki job_list --statuses open --limit 10
den-wiki job_claim --id 12 --harness codex
den-wiki wiki_read --path wiki/agent-memory.md --human
den-wiki cat wiki/agent-memory.md        # just the Markdown (wiki_read content_only)
den-wiki grep stamina --topic rusty-dagger --context 3   # matching passages (wiki_grep) as text
den-wiki wiki_read --path wiki/rusty-dagger/combat.md --section "Stamina and Fatigue"
den-wiki wiki_write --args-json @page.json
den-wiki wiki_attach --page wiki/agent-memory.md --name recall.png --content-base64 @recall.png --trail agent-memory-research-2026-10-28
```

Arguments are checked against the operation's schema before any request:
unknown fields (with a suggested name), missing required fields, wrong types
and a missing identity for a write are usage errors and make no call.

## Output and exit codes

JSON on stdout by default; `--human` prints readable key: value lines. Errors go
to stderr as `{"error": {"code", "message", "retryable"}}` (one line with
`--human`).

| Exit | Meaning | Codes |
|---|---|---|
| 0 | success | |
| 2 | usage error | `unknown_operation`, `unknown_field`, `invalid_arguments`, `missing_agent`, `validation_failed` |
| 4 | not found | `not_found` |
| 5 | conflict | `claim_conflict`, `lease_expired`, `invalid_transition`, `stale_write` |
| 6 | den-wiki or a backend unavailable | `backend_unavailable` |
| 7 | internal error | anything else |

`den-wiki lint` keeps its own codes: 1 means findings at or above `--fail-on`.

## Parity

`internal/cli/catalog.json` is generated from the operation registry
(`go generate ./internal/cli`). `TestCatalogParity` fails when it drifts from
the registry in either direction, with no allowlist, and `verify.sh` checks it
too. The OK integration test drives every catalog operation through the CLI
against a real facade, ledger and OpenKnowledge, and checks that the live
`tools/list` (full profile) matches the catalog.
