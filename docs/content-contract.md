# den-wiki content contract

This is the contract for `den-wiki-content`, the Markdown repository that
OpenKnowledge (OK) serves. It covers the layout, names, frontmatter, citations,
commit convention and trail format. `den-wiki lint` checks it mechanically; the
JSON Schemas in [`schemas/`](../schemas) are the source of truth for
frontmatter. The design rationale lives in Den document
`den-wiki/den-wiki-design`.

## Write policy

- Any agent may write to any zone. Every rule below is a lint **finding**, never
  a write block. Filling the wiki matters more than tidiness on the way in.
- Pages written during research start as `curation: unreviewed`. Readers and
  search can tell the difference, so writing loosely costs little.
- A scheduled librarian run curates: it merges duplicates and alias collisions,
  renames or merges badly named files, fixes dead links and orphans, scores
  staleness, splits oversized pages, folds trails' proposed changes into pages
  and promotes pages to `curated`. It may delete. It scores and reports first,
  then acts, and records itself as a `type: curation` trail
  (`trails/curation-<date>-...`).

## Layout

```
den-wiki-content/
  index.md          generated root index (frontmatter: okf_version only)
  taxonomy.yml      page types and tags
  .gitattributes    OpenKnowledge's generated-index block (required for indexes)
  .ok/              OpenKnowledge config, schema copies and .gitignore
  sources/          captured material, never edited
  trails/           one page per run (research, round synthesis, calibration, curation), append-only
  wiki/             one page per concept, the current understanding
```

Nothing else belongs at the root (`zone.outside`). Dot-files and dot-folders
(`.git`, `.ok`, `.gitignore`, `.gitattributes`) are tooling and are not
checked. The content template ships what `ok init` would otherwise create;
never run `ok init` on the content repo.

### Zones

| Zone | Holds | Mutability | Dates and IDs in names |
|---|---|---|---|
| `sources/` | Captured source material: articles, papers, transcripts, repo manifests | Never edited. A re-fetch is a new file with `supersedes:` | Allowed; subfolders allowed |
| `trails/` | One page per run: research, round synthesis, calibration or curation | Append-only | Allowed; no subfolders |
| `wiki/` | One page per concept, entity, comparison, synthesis or hub | Edited in place, merged and deleted freely | **Never** |

Events and state are kept apart. A concept has exactly one page under `wiki/`,
git holds that page's versions, and `trails/` holds the reasons for each
version. Never write state as if it were an event (`truth-<date>.md`,
`<task>/<run>/truth.md`).

Each zone folder carries `.ok/frontmatter.yml`, an open-shape map stating the
zone's rules (`zone`, `description`, `mutability`, `rules`, `template`,
`contract`). OK shows only the `description` when an agent lists a folder, so
the description carries each zone's essential rules in one paragraph; the
den-wiki facade adds the full `rules` list to its read and list results. Each
zone also has an OK per-folder template in `.ok/templates/` (`page`, `source`,
`trail`) to start new files from. A template's own menu title and description
nest under a `template:` key; every other key is the new document's starting
frontmatter. (A top-level `title` would make OK treat the whole block as
template metadata and start new documents with no frontmatter.)

Lint checks only `.md` files. OK stores pasted files in `<doc>.assets/` folders
beside the document; lint skips those folders. Other non-Markdown files are
reported once (`zone.non_markdown`) and not checked further.

A file whose frontmatter is shaped for another zone (for example a trail saved
under `wiki/`) is reported as `zone.misplaced` and not validated against the
folder's schema. Shapes are recognised by a zone's own `type` values (`source` and
`agent-output`; `trail`, `round-synthesis`, `calibration` and `curation`) or
by keys only one zone uses: `sha256`/`source_url`/`captured_by_trail` for
sources, `question` with `started`/`harness`/`job` for trails, and
`curation`/`confidence`/`volatility` for pages.

## Naming under wiki/

- File and folder names under `wiki/` say what the page is about, never when or
  by whom it was written.
- Names are lowercase slugs: `a-z`, `0-9` and single hyphens.
- No dates (`2026-10-29`, `20261029`, `2026-10`, `q3-2026`), no SHAs or hashes
  (a hex run of 7+ characters mixing digits and letters), no version tokens
  (`v2`, `rev3`) and no trailing status words (`-final`, `-new`, `-updated`,
  `-old`, `-latest`, `-deprecated`, `-wip`, `-backup` and similar). Status
  words count only at the end of a name, so `new-york` passes.
- `index.md` is reserved at every level for generated indexes.

## Frontmatter

Every page, source and trail starts with a YAML frontmatter block: a first
line of exactly `---`, YAML, and a closing line of exactly `---`. Unknown keys
are allowed (tools such as OK may add their own) unless a rule below names
them.

Dates are `YYYY-MM-DD` or an ISO 8601 date-time (`2026-10-29T09:15:00Z`).

### wiki/ pages: [`schemas/wiki-page.schema.json`](../schemas/wiki-page.schema.json)

| Field | Required | Meaning |
|---|---|---|
| `type` | yes | A type listed in `taxonomy.yml` (seed: `concept`, `entity`, `comparison`, `synthesis`, `hub`) |
| `title` | yes | Page title |
| `aliases` | | Alternative names. Search resolves aliases, so a new name lands on the existing page instead of spawning a duplicate |
| `tags` | yes | Tags listed in `taxonomy.yml` only; add new tags there first. May be empty |
| `sources` | | Content-root-relative paths under `sources/` |
| `trails` | | Slugs of the trails that changed this page |
| `curation` | yes | `unreviewed` or `curated` |
| `confidence` | | `high`, `medium` or `low` |
| `contradicts` | | Content-root-relative paths of `wiki/` pages with conflicting claims |
| `volatility` | | `hot`, `warm` or `cold`; drives staleness scoring. Missing means `warm` |
| `verified` | | Date of the last human confirmation. Only a human sets this |
| `updated` | yes | Date of the last change |

Two pages must not share a title or alias (`wiki.name_collision`), compared
case-insensitively; one concept has one page.

### sources/: [`schemas/source.schema.json`](../schemas/source.schema.json)

| Field | Required | Meaning |
|---|---|---|
| `type` | | `source` for material captured from outside, `agent-output` for output of our own agents captured from a run (see below). OKF wants a type on every document |
| `title` | | Title of the captured material |
| `source_url` | yes | Where it was captured from (a URI) |
| `captured` | yes | When it was captured |
| `sha256` | yes | Lowercase hex SHA-256 of the body, quoted |
| `captured_by_trail` | yes | Slug of the trail whose run captured it |
| `supersedes` | | Content-root-relative path of the earlier capture this one replaces |

The **body** is every byte after the line that closes the frontmatter. Store
the capture there unedited. When the hash does not match, lint reports the
value it computed, so an agent capturing a source can copy it from the finding.
Quote the hash in YAML (`sha256: "…"`) so an all-digit value stays a string.

`type: agent-output` marks a capture of our own agents' output, such as each
harness's report from a calibration run, captured from its run directory. It
records the run and is never external evidence: it supports claims about the
run (what a harness wrote, how it worked), not claims about the world, and the
curation scan does not report it as uncited. Sources are never edited, so a
capture made before `agent-output` existed is marked by capturing it again
with `type: agent-output` and `supersedes:` naming the old file.

### trails/: [`schemas/trail.schema.json`](../schemas/trail.schema.json)

| Field | Required | Meaning |
|---|---|---|
| `type` | | The kind of run (below). OKF wants a type on every document |
| `job` | | Research ledger job id; absent for runs not queued in the ledger |
| `question` | yes | The question the run set out to answer |
| `started` | yes | When the run started |
| `finished` | | When it ended; absent while it runs |
| `agent` | yes | Identity of the agent |
| `harness` | yes | Harness it ran in, for example `claude-code`, `codex`, `dsh`, `hermes` |

#### Trail kinds

A trail's `type` says what kind of run it records. Tooling groups and filters
trails by kind rather than by slug, and OK's generated `trails/index.md`
groups by `type`, so each kind gets its own section there and calibration and
curation runs do not bury research runs.

| `type` | Kind | Run |
|---|---|---|
| `trail` | research | A research run: one question, or one evidence slice of a research round. The default |
| `round-synthesis` | round-synthesis | The synthesis of a research round, reading its slice trails (the round's child jobs) |
| `calibration` | calibration | One prompt given unchanged to several harnesses to compare their approach. Read-only: it edits no pages except the calibration hub, and each harness's report is an `agent-output` source |
| `curation` | curation | A librarian curation pass ([curation](curation.md)) |

Set `type` when the trail is created. Trails are append-only, so a trail
written before kinds existed keeps `type: trail`; its kind is then read from
its slug, `curation-` or `calibration-` prefixed, and is research otherwise.
The `wiki_trails` operation ([facade](facade.md)) lists trails by kind with
a Markdown table for hub pages, and a research ledger job's `kind`
([ledger](ledger.md)) uses the same names, so a job says up front what its
trail will be.

### taxonomy.yml: [`schemas/taxonomy.schema.json`](../schemas/taxonomy.schema.json)

```yaml
version: 1
types:
  - name: concept
    description: An idea, technique or pattern, explained on its own terms.
tags:
  - name: ai
    description: AI models, agents and their tooling.
    parent: software   # optional; must be another tag
```

Names are slugs and appear once each. The type list is kept here rather than in
the page schema so the content repo can grow it without a code change.
Agents read the current taxonomy, parsed and with the pages using each name,
through the `wiki_taxonomy` operation ([facade](facade.md)).

## Citations

Claims cite sources with Markdown footnotes. A citation footnote links to a file
under `sources/` and quotes the span that supports the claim, in double quotes
(straight or curly) or as a `>` quote:

```markdown
External memory makes agents more consistent.[^1]

[^1]: [The survey](../sources/agent-memory-survey.md) "Agents that keep an external, editable memory answer follow-up questions more consistently"
```

Links may be relative to the page or root-relative (`/sources/...`). A footnote
may continue on indented lines. Lint checks that the linked source exists
(`citation.missing_source`) and that the footnote quotes something
(`citation.missing_quote`). Footnotes inside fenced code blocks are ignored.

The link text has at least two words (`citation.one_word_text`): OK reads
`[^1]: [Survey](path)` as a link reference definition and reports a bogus dead
link.

Link with Markdown inline links, relative or root-relative, not
`[[wikilinks]]` (OKF flags every wikilink as non-portable) and not
reference-style definitions (`[r]: path.md`), which OK's `move` does not
rewrite.

## Commit convention

Every content commit that changes `wiki/` carries `(trail:<slug>)` in its
subject, mirroring `(#task_id)` in code repos:

```
Add agent memory page (trail:agent-memory-research-2026-10-28)
```

OpenKnowledge never commits to the content repo (its own history lives in a
shadow repo under `.git/ok`), so the den-wiki facade makes these commits: every
`wiki_write`, `wiki_edit`, `wiki_move` and `wiki_delete` takes a `trail`, and
the facade commits the files it changed with the calling agent as author and
`<summary> (trail:<slug>)` as subject. OK's autoSync stays off; its
`Auto-save: N files changed` commits would carry neither.

`<slug>` is the trail's filename without `.md`, and `trails/<slug>.md` must exist
in that commit, so commit the trail with (or before) the pages it changed.
Following provenance is then: a page's git history → each commit → its trail →
the sources the trail accepted and rejected.

A person editing in OpenKnowledge's web UI has no trail. The facade commits
those changes in the background with `(ui)` in place of the trail tag, or
`(ui:format)` when the change only moves blank lines or trailing whitespace
(OK's editor re-saves pages in its own formatting when they are viewed). Lint
accepts both tags in place of `(trail:<slug>)`, and a `(ui:format)` commit
cannot rewrite a trail. A UI edit that changes a source or removes lines from
a trail is still reported.

`den-wiki lint --commits <range>` also reports commits in the range that modify
an existing source (`commit.source_edited`) or remove lines from or delete a
trail (`commit.trail_rewritten`).

All commit checks look only at pages, sources and trails: the `.md` files lint
checks in the tree. Generated `index.md` files, anything in a dot-folder such as
`.ok/`, and OK `<doc>.assets/` folders are exempt in every zone, so regenerating
indexes, editing zone rules or templates, or replacing an asset needs no trail
tag and is not an edit to a source or trail.

## Delete or merge, never relabel

History lives in git. A superseded page is merged into its successor (move the
content, rewrite the links, delete the old file) or deleted. It is never
renamed `-old`, given a `deprecated`, `superseded`, `superseded_by`,
`supersedes`, `obsolete`, `replaced_by` or `status` key, or given a title that
starts with `DEPRECATED`, `Obsolete`, `Superseded`, `Outdated` or `Old`
(`wiki.relabelled`). `supersedes` belongs to sources only.

## Generated indexes

Each folder's `index.md` is machine-maintained by OK's OKF index generation
(`contentRules.okf.generate.index` in `.ok/config.yml`) and never hand-written.
Generation stays paused until the content root has OK's `.gitattributes`
block (in the template). OK rewrites the root `index.md` too, keeping its
`okf_version`. Following OKF, index files carry no frontmatter, except
`okf_version` in the root `index.md` (`index.frontmatter`). Lint does not check
index content. OK leaves regenerated indexes uncommitted; the den-wiki facade
commits them in their own untagged commits, which the commit checks exempt.

Each document's `type` groups it in its folder's index. OKF requires a `type`
on every document, so sources carry `type: source` (or `agent-output`) and
trails their kind (`type: trail` for research). Both are optional in the
schemas and set by the templates.

## Trail page format

A trail is one run of any kind, at `trails/<slug>.md`. Trails are
flat so a slug names exactly one file. The filename may carry dates and IDs.
Append while the run goes on; never remove or rewrite what a trail records.

The body has these `##` sections, all present even when empty ("None."):

1. **Question and scope**
2. **Prior coverage checked:** wiki pages and earlier trails consulted before
   searching
3. **Queries and methods:** each query or method, the tool used, a result
   summary and a verdict
4. **Sources:** accepted (with a credibility note) and rejected (with a reason)
5. **Dead ends:** methods or directions that went nowhere, and why
6. **Changes made:** links to the pages created or edited
7. **Proposed changes:** for the librarian, where the agent did not edit
   directly
8. **Open questions and gaps**

Headings match case-insensitively and may carry a number (`## 5. Dead ends`).

## Lint

```bash
den-wiki lint [--format json|text] [--fail-on info|warning|error] [--commits <range>] <content-dir>
den-wiki lint --list-rules
```

Output is JSON by default (`root`, `commits`, `counts`, `findings`); `--format
text` is for people. Each finding has a stable `code`, a `severity`, a `path`
(content-root-relative) and, where it applies, a `line` or `commit`. Messages
are for people and may change; switch on codes.

Exit codes: `0` ran (findings or not), `1` a finding at or above `--fail-on`,
`2` usage error, `3` content or git could not be read. Without `--fail-on` the
exit code never reflects findings, because rules are findings, not write blocks.

| Code | Severity | Meaning |
|---|---|---|
| `zone.outside` | warning | A top-level entry is not a zone, `index.md` or `taxonomy.yml` |
| `zone.missing` | info | A zone folder does not exist |
| `zone.rules_missing` | info | A zone folder has no `.ok/frontmatter.yml` |
| `zone.misplaced` | warning | A file's frontmatter belongs to another zone |
| `zone.non_markdown` | info | A non-Markdown file outside an `.assets/` folder |
| `zone.nested_trail` | warning | A trail is in a subfolder |
| `naming.slug` | warning | A `wiki/` name is not a lowercase slug |
| `naming.date` | warning | A `wiki/` name contains a date |
| `naming.sha` | warning | A `wiki/` name contains a SHA or hash |
| `naming.version_marker` | warning | A `wiki/` name carries a version or status marker |
| `frontmatter.missing` | error | No frontmatter block |
| `frontmatter.invalid` | error | Frontmatter is unclosed, not YAML, or not a mapping |
| `frontmatter.schema` | error | Frontmatter does not match its zone's schema; one finding per violation |
| `index.frontmatter` | warning | A generated `index.md` carries frontmatter |
| `taxonomy.missing` | error | No `taxonomy.yml`; tag and type checks are off |
| `taxonomy.invalid` | error | `taxonomy.yml` does not match its schema |
| `taxonomy.duplicate` | warning | A tag or type is listed twice |
| `taxonomy.unknown_parent` | warning | A tag's parent is not a tag |
| `taxonomy.unknown_tag` | warning | A page uses a tag not in the taxonomy |
| `taxonomy.unknown_type` | warning | A page uses a type not in the taxonomy |
| `ref.missing_source` | warning | A page's `sources` entry does not exist |
| `ref.missing_trail` | warning | A `trails` or `captured_by_trail` entry has no trail file |
| `ref.missing_page` | warning | A `contradicts` entry does not exist |
| `wiki.relabelled` | warning | A page is labelled old instead of merged or deleted |
| `wiki.name_collision` | warning | Two pages share a title or alias |
| `citation.missing_source` | warning | A citation links to a missing source |
| `citation.missing_quote` | warning | A citation does not quote its span |
| `citation.one_word_text` | warning | A citation footnote starts with one-word link text |
| `source.sha256_mismatch` | error | A source body does not match its `sha256` |
| `source.supersedes_missing` | warning | A `supersedes` target does not exist, or is the file itself |
| `trail.section_missing` | warning | A trail lacks a required section |
| `commit.missing_trail_tag` | warning | A commit changes `wiki/` without `(trail:<slug>)`, `(ui)` or `(ui:format)` |
| `commit.unknown_trail` | warning | A commit's trail tag names a trail absent from that commit |
| `commit.source_edited` | warning | A commit modifies an existing source |
| `commit.trail_rewritten` | warning | A commit removes lines from or deletes a trail |

Codes are never renamed or reused; a changed rule gets a new code.
