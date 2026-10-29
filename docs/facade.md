# den-wiki facade

`den-wiki serve --config <file>` runs one process with:

- `/mcp`: the den-wiki MCP endpoint (JSON-RPC over HTTP POST, stateless, JSON
  responses, no SSE);
- `/v1/jobs`: the research ledger's HTTP API ([ledger.md](ledger.md));
- `/healthz` (ledger and OpenKnowledge checks) and `/version` (commit and
  catalog revision).

Configuration is a YAML file ([example](../configs/den-wiki.example.yml)),
never environment variables. The listen address must be loopback or private
LAN: den-wiki has no auth of its own.

## Ownership

The facade owns tool names, schemas and descriptions, discovery profiles, the
catalog revision, aliases and routing. It owns no domain state: jobs live in
the ledger, content lives in the den-wiki-content repo that OpenKnowledge (OK)
serves. OK is a pinned npm package (`scripts/ok-version`) run unmodified and
reached only over its MCP endpoint; it is GPL-3 and never vendored or linked.

## Operations

One registry (`internal/registry`) defines every operation; MCP `tools/list`
and the CLI catalog are both generated from it. Each operation's input schema
is generated from its typed argument struct and arguments are decoded strictly
into that struct, so unknown fields, missing required fields and a missing
agent identity are rejected before any backend is called.

| Operation | Risk | Discovery | Backend |
|---|---|---|---|
| `wiki_search` | read | default | OK `search` |
| `wiki_read` | read | default | OK `exec cat` briefing, file from the content dir |
| `wiki_write` | write | default | OK `write`, then a den-wiki commit |
| `wiki_edit` | write | default | OK `edit`, then a den-wiki commit |
| `wiki_list` | read | long tail | OK `exec ls` |
| `wiki_links` | read | long tail | OK `links`, filtered |
| `wiki_history` | read | long tail | OK `history` |
| `wiki_audit` | read | long tail | OK `audit` |
| `wiki_move` | write | long tail | OK `move`, then a den-wiki commit |
| `wiki_delete` | destructive | long tail | OK `delete`, then a den-wiki commit |
| `job_create`, `job_list`, `job_claim`, `job_finish` | write/read | default | ledger |
| `job_get`, `job_events`, `job_renew`, `job_release`, `job_edit`, `job_abandon` | read/write/destructive | long tail | ledger |
| `wiki_lint`, `wiki_lint_rules` | read | long tail | den-wiki lint on the content dir |

`tools/list` lists the default tier. Send `X-Den-Wiki-Tool-Profile: full` (or
`params.toolProfile: "full"`) to list everything. Long-tail operations are
always callable by exact name. Promoting one to the default tier is a
per-operation decision backed by usage evidence from the telemetry.

The passthrough is a thin reshape (design open decision 1, settled by #10462):
OK's skills, palette, config, preview, share, checkpoint and restore tools are
not exposed.

## Agent identity

Callers send their identity in the `X-Den-Wiki-Agent` header. It is the
ledger actor (no ledger tool takes an actor argument), the git author of
content commits, and the OK writer: OK attributes writes per MCP session, so
the facade keeps one OK session per agent, initialised with
`clientInfo.name = den-wiki/<agent>` and `x-ok-connection-id: den-wiki-<agent>`,
and re-initialises when OK drops a session (30-minute TTL). Read operations
work without an identity; write and destructive operations refuse to run
without one (`missing_agent`).

## Content commits

OK never commits to the content repo; its history lives in a shadow repo under
`.git/ok`. So every wiki write takes a required `trail` (the slug of the run's
trail page). Write operations run one at a time: each holds a lock from its
OK call through its commit, so another agent's change to the same file (or a
move rewriting it) cannot land in between and be committed under the wrong
author. After OK applies a write the facade:

1. waits (up to 10 s) until the change is on disk;
2. commits exactly the touched files (for a move, also every document OK
   rewrote links in) on top of HEAD, built in a temporary index so other
   agents' uncommitted work is never swept in, with the agent as author and
   `<summary> (trail:<slug>)` as subject;
3. commits any `index.md` files OK regenerated, in their own untagged commit
   (only those paths; anything else staged stays staged).

The result carries `commit` (sha, subject, author, and `index_commit_error`
if step 3 failed), or `commit_error` when the change was applied in OK but not
committed. Frontmatter is always sent inside
the content (OK's separate frontmatter argument drops empty arrays).

OK reconciles these commits into its own history under the git author, and its
read briefing (`wiki_read`'s `project_history`) lists them with their
trail-tagged subjects. Pushing the content repo is the deployment's job.

## Responses and errors

Successful calls return the result as `structuredContent` (and as JSON text).
Failed calls return `isError: true` with
`structuredContent.error = {code, message, retryable}`. Codes are stable:
`unknown_operation`, `unknown_field` (with a suggested field name),
`invalid_arguments`, `missing_agent`, `not_found`, the ledger's
`invalid_transition`, `claim_conflict`, `lease_expired` and
`validation_failed`, `stale_write`, `backend_unavailable` (retryable),
`backend_error` and `internal_error`.

`wiki_read` and `wiki_links` drop OK artefacts: templates and anything under
dot-folders, generated indexes, hubs that are only dead targets, and OK's
misparse of one-word footnote link text.

## Telemetry and the read log

Every `tools/call` logs one `mcp_tool_call` JSON line with `requested_tool`,
`canonical_tool`, `agent`, `outcome`, `retryable` and `duration_ms`.
Arguments, content and backend error bodies are never logged.

Every read operation also appends a durable read-log row (SQLite table
`read_log` in the service database): time, agent, tool, query, and the page
paths returned and opened. It is training data for the future routing
classifier. The query is the search text, the listed folder, or the link
kinds followed by the page asked about (`backlinks,forward
wiki/agent-memory.md`). Returned paths are the existing pages the response
named (search results, listed documents, link sources and targets), as `.md`
paths, without templates, generated indexes or dead link targets.

## Tests

`go test ./...` covers the registry, the MCP handler, the ledger and lint
tools and the committer. The OpenKnowledge integration test
(`internal/facade/ok_integration_test.go`, build tag `okintegration`) starts
the pinned OK on a copy of the content template and checks read with links,
search, list, per-agent write and edit commits, move, delete, link filtering,
a ledger claim and finish against a real trail page, unknown-field rejection
with zero OK calls, and read-log rows. CI runs it as the `ok-integration` job:

```bash
scripts/ok-integration.sh /tmp/openknowledge --isolate-home
```

`--isolate-home` runs OK under bwrap with a throwaway `$HOME`, because OK
writes `~/.ok/local` under the real home directory whatever `HOME` says.
