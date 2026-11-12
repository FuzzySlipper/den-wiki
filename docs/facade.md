# den-wiki facade

`den-wiki serve --config <file>` runs one process with:

- `/mcp`: the den-wiki MCP endpoint (JSON-RPC over HTTP POST, stateless, JSON
  responses, no SSE);
- `/v1/jobs`: the research ledger's HTTP API ([ledger.md](ledger.md));
- `/v1/export` and `/v1/export/asset`: one wiki page rendered for sharing,
  and its images ([Sharing a page](#sharing-a-page));
- `/healthz` (ledger and OpenKnowledge checks) and `/version` (commit and
  catalog revision);
- every other path: the read-only reader ([reader.md](reader.md)), when
  `content_dir` is set.

Configuration is a YAML file ([example](../configs/den-wiki.example.yml)),
never environment variables. The listen address must be loopback or private
LAN: den-wiki has no auth of its own.

## Ownership

The facade owns tool names, schemas and descriptions, discovery profiles, the
catalog revision, aliases and routing. It owns no domain state: jobs live in
the ledger, content lives in the den-wiki-content repo that OpenKnowledge (OK)
serves. OK is a pinned npm package (`scripts/ok-version`) reached over its MCP
endpoint.

## Operations

One registry (`internal/registry`) defines every operation; MCP `tools/list`
and the CLI catalog are both generated from it. Each operation's input schema
is generated from its typed argument struct and arguments are decoded strictly
into that struct, so unknown fields, missing required fields and a missing
agent identity are rejected before any backend is called.

| Operation | Risk | Discovery | Backend |
|---|---|---|---|
| `wiki_search` | read | default | OK `search` |
| `wiki_read` | read | default | OK `exec cat` briefing, file from the content dir; `content_only` returns path and content from the content dir alone, without calling OK |
| `wiki_write` | write | default | OK `write`, then a den-wiki commit |
| `wiki_edit` | write | default | OK `edit`, then a den-wiki commit |
| `wiki_attach` | write | default | OK `write` (asset) and a den-wiki commit, or the media store; see [Media](#media) |
| `wiki_list` | read | long tail | OK `exec ls` |
| `wiki_links` | read | long tail | OK `links`, filtered |
| `wiki_history` | read | long tail | OK `history` |
| `wiki_audit` | read | long tail | OK `audit` |
| `wiki_move` | write | long tail | OK `move`, then a den-wiki commit |
| `wiki_delete` | destructive | long tail | OK `delete`, then a den-wiki commit |
| `job_create`, `job_list`, `job_claim`, `job_finish` | write/read | default | ledger |
| `job_get`, `job_events`, `job_renew`, `job_release`, `job_edit`, `job_abandon` | read/write/destructive | long tail | ledger |
| `wiki_taxonomy` | read | default | `taxonomy.yml` from the content dir, parsed, with tag and type use counted across `wiki/` |
| `wiki_lint`, `wiki_lint_rules` | read | long tail | den-wiki lint on the content dir |
| `wiki_curation_scan` | read | long tail | `lint.Curate` on the content dir plus OK `links` (dead, orphans); see [curation.md](curation.md) |
| `wiki_trails` | read | long tail | `lint.ListTrails` on the content dir: trails by kind |
| `wiki_export` | read | long tail | `export.Export` on the content dir: one page rendered for outside readers |
| `wiki_publish` | write | long tail | den-services doc-publish; see [Sharing a page](#sharing-a-page) |

`tools/list` lists the default tier. Send `X-Den-Wiki-Tool-Profile: full` (or
`params.toolProfile: "full"`) to list everything. Long-tail operations are
always callable by exact name. Promoting one to the default tier is a
per-operation decision backed by usage evidence from the telemetry.

`wiki_trails` lists trails by kind (`research`, `round-synthesis`,
`calibration`, `curation`; see the content contract's trail kinds), newest
first: each with `path`, `slug`, `kind`, `question`, `started`, `finished`,
`agent`, `harness` and `job`, plus `counts` for every kind and
`listing_markdown`, a table a hub page or trail can carry. OK's generated
`trails/index.md` already groups trails by `type` for people; this is the
same grouping for agents, with the legacy slug inference applied.

`wiki_taxonomy` is its own operation rather than letting `wiki_read` open
`taxonomy.yml` (#10508). `wiki_read` reads pages through OK, and OK serves
Markdown only; the taxonomy is YAML in the content dir, and agents want it
parsed, with the pages using each tag and the tags pages use without a
taxonomy entry. It starts in the default tier because every research run
needs it before writing a page: in #10467 all four harness runs looked for the
tag list and found none. The result: `present`, `version`, `types`, `tags` and
`topics` (each `name`, `description`, `parent` for tags and topics, and
`pages`), `unlisted_types`, `unlisted_tags` and `unlisted_topics` (`name`,
`pages`), `unsorted` (the pages with no topic), and the `taxonomy.*` lint
`findings`. Lint stays the enforcement: an unknown tag is still a
`taxonomy.unknown_tag` finding, never a write error.

The passthrough is a thin reshape (design open decision 1, settled by #10462):
OK's skills, palette, config, preview, share, checkpoint and restore tools are
not exposed.

## Media

`wiki_attach` attaches an image, animated GIF, clip or PDF to a page, source
or trail (`page`, which need not exist yet), at full size: the file travels
base64 encoded (`content_base64`, up to 96 MiB; the MCP request limit is set
from that), and den-wiki never resizes or recompresses it. The CLI reads a
local file for `--content-base64 @<path>`.

- **Into git** (images and PDFs up to `media.git_max_bytes`, default 50 MiB):
  through OK's `write` with an `asset`, into `<doc>.assets/<name>`, then
  committed like any write: the agent as author, `Attach <path>
  (trail:<slug>)`. OK keeps one copy of identical bytes in a folder; when it
  already holds them it writes nothing and answers with the existing file,
  which the result reports (`deduplicated`).
- **Into the media store** (video, anything over the limit, or `store:
  media`): `<sha256>.<ext>` in `media.dir`, written through a temporary file
  and never changed; no commit. The store is served by `den-wiki serve` at
  `GET /media/<sha256>.<ext>` with range requests (so video seeks), the
  file's MIME type, an immutable cache life, and a sandboxing
  Content-Security-Policy so an SVG opened on its own runs no script.

The result: `stored` (`git` or `media`), `path` or `url`, `sha256`, `bytes`,
`mime`, the `commit`, and `embed`, the Markdown or JSX to paste (relative to
the page for git files). Accepted types: png, jpg, jpeg, gif, webp, avif,
svg, mp4, webm, pdf.

OK knows nothing of the store and reports `/media/` images and links as
dead. The facade drops those from write results and link views when the store
holds the file, and also drops a write's broken link to a file that is on
disk: OK's link index can lag a file attached a moment before.

## Sharing a page

`wiki_publish` shares one wiki page outside the LAN as a post on Patch's blog,
the way Den documents are shared with "share via blog" (#10550). It replaces
OK's Share button for this: that pushes the whole repository to GitHub, and
its links send readers to download the OK app. OK has no setting to hide the
button, and den-wiki never patches OK, so sharing is an agent and CLI action
(`den-wiki wiki_publish --path wiki/x.md`), not a button.

den-services doc-publish stays the only writer of the blog repository. den-wiki
renders the page and asks:

1. `wiki_publish` checks the page exports, then posts
   `{"source": {"wiki_page": "wiki/x.md"}, "options": {...}, "requested_by": <agent>}`
   to doc-publish's `/preview` (default) or, with `publish: true`, its
   publications endpoint (`publish.doc_publish_url`). doc-publish's answer is
   the result; its errors come back as `backend_error`, or `conflict` for a
   blog repository that fails its safety checks.
2. doc-publish fetches the page itself from `GET /v1/export?page=wiki/x.md&asset_base=<url>`
   and each listed image from `GET /v1/export/asset?page=wiki/x.md&name=<name>`
   (bytes, with `X-Content-SHA256`), copies the images beside the post and
   commits both.

The export (`internal/export`, also the `wiki_export` operation) is the page
alone, never the pages it links to. It returns `path`, `title`, `updated`,
`markdown`, `assets` (`name`, `path`, `sha256`, `size`, `content_type`) and
`warnings`. Only `wiki/` pages export. The Markdown:

- drops the frontmatter, and a first `# heading` that repeats the title (the
  blog layout prints it);
- links citations, and any other link to a source, to the source's
  `source_url`; a source without one leaves its link text, with a warning;
- makes links to other wiki pages and trails plain text: they are LAN-only;
- lists the images and files (PDF) the page shows from the content repo as
  assets, named uniquely within the page, and points their links at
  `asset_base` + name; `width={600}` becomes `width="600"`;
- replaces a video with an italic "Video not included" line, with a warning:
  the media store is LAN-only and clips are large;
- turns `> [!NOTE]` callouts into `> **Note:**`, since the blog's Markdown
  (kramdown) has no callouts;
- leaves fenced code and code spans exactly as written, and warns when the
  page has a Mermaid diagram or display math, which the blog shows as source
  unless its theme renders them.

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
committed yet. The UI reconciler (below) retries such a commit under the same
agent and subject. Frontmatter is always sent inside
the content (OK's separate frontmatter argument drops empty arrays).

OK reconciles these commits into its own history under the git author, and its
read briefing (`wiki_read`'s `project_history`) lists them with their
trail-tagged subjects. Pushing the content repo is the deployment's job.

### Changes made in the UI

OK's web UI writes straight to the content directory. So does its visual
editor when someone only views a page: it re-saves the page in its own
Markdown formatting (blank lines between footnote definitions, none after the
frontmatter). The UI reconciler (`Committer.ReconcileUI`, run by `den-wiki
serve` every `ui_commits.interval`) commits these changes so they reach
history and the backup. It takes the same write lock as the wiki operations,
so it never runs inside a facade write. Each pass looks at the files under
`sources/`, `trails/` and `wiki/`, and `taxonomy.yml`, that differ from HEAD
(generated `index.md` files excepted). `taxonomy.yml` is included because no
wiki operation writes it: Patch's approved taxonomy changes are made in OK's
editor, and nothing else would commit them (#10561). Each pass then:

1. commits paths a facade write left uncommitted under that write's agent and
   subject, once they reach disk. Such a path stays pending until a commit of
   it lands, whether this one or a later facade write's;
2. leaves a file changed within `ui_commits.quiet` for a later pass, so a page
   being typed into is committed once, not on every save;
3. commits the changes that only move blank lines or trailing whitespace
   together, subject
   `Formatting only, re-saved by the OpenKnowledge editor: <paths> (ui:format)`;
4. commits everything else together, subject
   `Edit in the OpenKnowledge UI: <paths> (ui)`.

It never reverts anything. It cannot tell a person's edit from an editor
re-save, so a source re-saved by viewing it is committed too, and lint reports
the changed capture (`source.sha256_mismatch`, `commit.source_edited`).

UI commits are authored by `ui_commits.author` (default `ui`, so
`ui <ui@den-wiki.local>`). The `(ui)` and `(ui:format)` tags stand in for
`(trail:<slug>)`; see the commit convention in the
[content contract](content-contract.md). A UI edit to the same file as a
concurrent agent write is committed under the agent, because OK merges both
into one file before the facade commits it.

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
