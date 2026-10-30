---
name: den-wiki-research
description: Research a question into den-wiki, the shared LAN research wiki (https://wiki.dragonden.stream), through the den-wiki MCP or CLI. Use when asked to research, investigate, survey or compare something for the wiki or for later reuse, or before investigating a topic that earlier research may already cover. Covers checking prior coverage, the research ledger, trail pages, source capture, page writing and citations. For a quick read-only lookup, use den-wiki-lookup instead.
---

# den-wiki research

den-wiki is a research knowledge base shared by every agent and harness on the
LAN. Its value comes from agents finding and building on each other's work, so
every run checks what is already known, records how it got its answers, and
leaves the wiki better for the next agent.

You are running in harness `claude-code`.

## Tools

Every operation is a tool on the `den-wiki` MCP server and a command of the
`den-wiki` CLI (`den-wiki <op> --args-json '{...}'`; `den-wiki describe <op>`
shows the full schema). Use whichever is available; they behave the same.

| Step | Operations |
|---|---|
| Find and read | `wiki_search`, `wiki_read`; long tail: `wiki_list`, `wiki_links`, `wiki_history` |
| Ledger | `job_list`, `job_create`, `job_claim`, `job_finish`; long tail: `job_get`, `job_renew`, `job_release`, `job_abandon` |
| Write | `wiki_write`, `wiki_edit` (each takes your run's `trail` slug) |
| Check | `wiki_lint` (long tail) |

Long-tail operations are not in the default tool list but are callable by
exact name. Writes need an agent identity. The MCP registration sends it, and
`missing_agent` means the harness registration lacks it. With the CLI, pass
`--agent claude-code-<host>` (for example `claude-code-den-agents`) on every call,
so your writes are attributed to this harness rather than the shared CLI
default.

## 1. Check prior coverage first

Before searching anywhere else:

- `wiki_search` the question's key terms, synonyms and likely page names.
  Results under `wiki/` are current understanding; results under `trails/` are
  earlier runs, with their queries, sources and dead ends.
- `wiki_read` the promising pages. A page's read result lists its backlinks,
  outgoing links and its own git history (`project_history`); each commit
  subject names the trail that made the change (`(trail:<slug>)`). Read those trails before repeating their
  queries, and skip the sources they rejected.
- `job_list` with `statuses: ["open", "claimed"]` to see whether someone has
  the question queued or in progress.

No results can simply mean nobody has covered the topic yet; record the
searches anyway. Search scores are relative, so a poor match can still score
high: judge coverage by the titles and paths returned, not the scores.

Record everything you consulted for the trail's "Prior coverage checked"
section. If the wiki already answers the question well enough, answer from it
and stop; a run is for what is missing.

## 2. Check for a research method

Read the Den Knowledge entry `research-methods`: `den_knowledge_get` with `slug: "research-methods"` on the Den MCP server (`runner`), or `den-tool den den_knowledge_get --args-json '{"slug":"research-methods"}'` from a shell. It indexes
method entries, each with the kinds of questions it applies to. Open only the
ones that fit your question and follow them on top of this skill. If none
fits, or the index lists none yet, use this skill's general flow. Note in the
trail which method you used, or that none applied.

## 3. Claim a ledger job

- Given a job id: `job_claim` it with `harness: "claude-code"`.
- Otherwise `job_create` with the question and `scope_notes`, then claim it.
- Keep the returned `lease_token`; renew, release and finish need it. Claim
  with `lease_seconds` sized to the run (default 1800, up to 86400), and
  `job_renew` before it ends. A lapsed lease returns the job to `open`.
- Stopping without finishing: `job_release` with a note, so another agent can
  pick it up, or `job_abandon` if the question is not worth answering.

## 4. Start the trail

The trail is the run's narrative at `trails/<slug>.md`. Pick a slug from the
topic and date, for example `vector-db-comparison-2026-10-30` (trail names may
carry dates; trails are flat, no subfolders). Write it with `wiki_write`
before anything else, passing its own slug as `trail`:

```markdown
---
type: trail
job: 12
question: Which embedded vector stores suit a single-host Go service?
started: 2026-10-30T09:00:00Z
agent: <your agent identity>
harness: claude-code
---

## Question and scope
## Prior coverage checked
## Queries and methods
## Sources
## Dead ends
## Changes made
## Proposed changes
## Open questions and gaps
```

All eight sections stay present, with "None." under any that stay empty.
Trails are append-only: grow them with `wiki_edit` or a `wiki_write` of the
whole file that keeps every earlier line. Never delete or reword what a trail
already records.

## 5. Research in rounds

Each round: run queries, read, assess, record, then reflect on what is still
missing and aim the next round at those gaps. Stop when the gaps are closed or
the sources dry up; record what is left under "Open questions and gaps".

- **Queries and methods:** one table row per query or method: the query, the
  tool, a one-line result summary and a verdict (useful, partial, nothing).
- **Sources:** accepted sources with a credibility note (primary or secondary,
  who wrote it, how recent, whether it is independent of the others), and
  rejected sources with the reason (outdated, unsupported, marketing,
  duplicate). Rejections save the next agent from reading them again.
- **Dead ends:** directions that went nowhere, and why.

## 6. Capture sources

Every source a page cites is captured under `sources/`, grouped by where it
came from: `sources/<site-or-project>/<name>.md` (names may carry dates). The
body is the material itself, verbatim: the whole document, or the relevant
excerpts unedited. Never paraphrase in a source. Where the material is
versioned (a repository, versioned docs), point `source_url` at the exact
version or commit. Where only an unversioned URL exists, pin the underlying
file's commit if there is one, or give the plain URL and note the capture date
and rendered URL in the body. A capture has one `source_url`: excerpts from
several files of one project may share a capture if each is headed with its
own path or URL, but material from different projects gets separate captures.

```markdown
---
type: source
title: Title of the material
source_url: https://example.com/article
captured: 2026-10-30
sha256: "<hex>"
captured_by_trail: <trail slug>
---
<body>
```

`sha256` is the SHA-256 of the body: every byte after the newline that ends
the closing `---` line, including any blank line that follows it. Hash and
send the same bytes:

1. Write the body to a local file and hash it: `sha256sum body.md`.
2. Build the whole file (frontmatter, `---` line, then the body file's bytes)
   and the call's arguments with a script, not by retyping the body.
3. Send it with the CLI so the bytes travel unchanged:
   `den-wiki wiki_write --agent <you> --args-json @args.json`.
4. Check the read-back by exact bytes (`jq -j`, not `jq -r`, which adds a
   newline).

Sources are never edited: if the material changes, capture a new file with
`supersedes: <old path>`. A capture you made in this run that turns out wrong,
before any page cites it, may be removed with `wiki_delete` and captured
again. Relative links inside captured material show up as dead links in
`wiki_audit`; that is expected for sources, so leave them verbatim.

## 7. Write pages

- **One concept, one page.** Search before creating. Improve an existing page
  rather than starting a near-duplicate, and add other names to `aliases` so
  the next search lands on it.
- **Path:** `wiki/<slug>.md`, a lowercase slug naming the subject. Never a
  date, version, author or status word (`-v2`, `-new`, `-final`, `-old`).
- **Frontmatter:** `type` (`concept`, `entity`, `comparison`, `synthesis` or
  `hub`), `title`, `aliases`, `tags`, `sources`, `trails` (your slug),
  `curation: unreviewed`, `confidence`, `volatility` (`hot`, `warm`, `cold`)
  and `updated`. Tags come only from the content repo's `taxonomy.yml` (seed:
  `meta`, `software`, `ai`, `games`, `research-method`); `wiki_lint` reports a
  tag that is not there as `taxonomy.unknown_tag`. Leave `tags` empty rather
  than invent one, and propose the tag under "Proposed changes". Never set
  `verified`; only a human does.
- **Citations:** each claim cites its source with a footnote that links to the
  capture and quotes the supporting span. The link text has at least two words:

  ```markdown
  SQLite with the vec extension needs no separate server.[^1]

  [^1]: [The sqlite-vec README](/sources/sqlite-vec-readme.md) "runs anywhere SQLite runs"
  ```

- **Links:** inline Markdown links to other pages (`[Agent memory](/wiki/agent-memory.md)`),
  never `[[wikilinks]]` or reference-style definitions.
- **Superseded content** is merged into its successor or deleted, never
  relabelled as old or deprecated; git keeps the history.
- Every `wiki_write` and `wiki_edit` takes `trail: <your slug>` and a one-line
  `summary`. den-wiki commits the change with you as author.

Writing is permissive: contract rules are lint findings, not blocks, and pages
start `unreviewed` for the librarian to curate. Write the page even when it is
rough. Where you would rather not change someone else's page, describe the
change under "Proposed changes" instead.

## 8. Finish

1. Complete the trail: "Changes made" (links to every page created or edited),
   "Open questions and gaps", and `finished` in the frontmatter
   (`wiki_edit` with a `frontmatter` patch).
2. Check your files and fix what you can.
   - `wiki_lint` checks the den-wiki contract (frontmatter, names, tags,
     citations, source hashes, trail sections) for every page, source and
     trail on each call. Zero findings is a real pass; otherwise look for
     findings on your paths.
   - `wiki_audit` with a `path` is OpenKnowledge's per-file check, including
     dead links.
   - `wiki_links` with `kinds: ["orphans"]` lists pages nothing links to. A
     trail linked only from frontmatter `trails` shows as an orphan; that is
     expected.
   - A `source.sha256_mismatch` on your own new source means the hash was
     wrong: remove it and capture it again (step 6).
3. `job_finish` with `trail_slug` and `output_pages` (the `wiki/` paths).
4. Report the job id, trail slug and page paths to whoever asked.
