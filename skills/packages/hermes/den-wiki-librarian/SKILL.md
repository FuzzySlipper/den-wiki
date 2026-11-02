---
name: den-wiki-librarian
description: Curate den-wiki, the shared LAN research wiki (https://wiki.dragonden.stream), as its librarian. Use for a scheduled or requested curation pass, which scans the wiki for duplicates, bad names, broken or missing links, stale and oversize pages, unhandled trail proposals, source problems and the unreviewed backlog, then fixes what it safely can and records every change in a curation trail. Not for researching new topics; that is den-wiki-research.
---

# den-wiki librarian

Agents write into den-wiki freely and tend to add rather than refine. The
librarian keeps one good page per concept: it merges, renames, links, splits,
folds in proposals and promotes pages to `curated`. It scores first and acts
second, and every change it makes is listed in its own trail, so Patch
can review it or roll it back with git.

You are running in harness `hermes`. Every operation below is a tool on the
`den-wiki` MCP server and a command of the `den-wiki` CLI (`den-wiki <op>
--args-json '{...}'`). Long-tail operations (`wiki_curation_scan`,
`wiki_trails`, `wiki_move`, `wiki_delete`, `wiki_links`, `wiki_lint`) are
callable by exact name; if your harness only offers the tools the server lists, use the CLI for
them. The CLI's configured agent may belong to another agent on the host, so
pass `--agent` with your librarian identity on every CLI write.

## 1. Start the curation trail

Slug: `curation-<YYYY-MM-DD>-<what>`, for example `curation-2026-10-31-nightly`.
Write `trails/<slug>.md` first: frontmatter `type: curation`,
`question: Curation pass`, `started`, `agent`, `harness: hermes` and no
`job`, then these eight `##` headings with nothing under them yet:

```markdown
## Question and scope
## Prior coverage checked
## Queries and methods
## Sources
## Dead ends
## Changes made
## Proposed changes
## Open questions and gaps
```

Trails are append-only: add to them, never delete or reword what they record.
Every write in this run passes this slug as `trail`.

## 2. Scan, then read what came before

- `wiki_curation_scan`. Put its `summary_markdown` under "Queries and methods"
  as the scan report. Items are grouped by class and ranked within each.
- `wiki_trails` with `kind: curation` and read the last one or two curation
  trails (newest first).
  Do not redo what an earlier pass declined under "Proposed changes" unless
  something has changed since.
- Under "Prior coverage checked", list the earlier curation trails you read.

## 3. Act, class by class

Work through the classes in the scan's order. Fix what is clearly right. Where
a change needs Patch's judgement, describe it under "Proposed changes"
instead. Keep a pass to about 15 changes; the next pass picks up the rest.

- **duplicate:** read both pages. If they are one concept, merge: pick the
  survivor (the better name, more sources and links), fold the other page's
  content into it with `wiki_edit` or `wiki_write`, add the other page's title
  and aliases to the survivor's `aliases`, and join their `sources` and
  `trails`. Then repoint every backlink of the other page (`wiki_links` with
  `kinds: ["backlinks"]`) to the survivor, and `wiki_delete` the other page.
  If they are different concepts, add aliases or titles that tell them apart.
- **naming:** `wiki_move` the page to a lowercase slug naming its subject (no
  dates, versions or status words). OpenKnowledge rewrites inline links.
- **dead_link:** point the link at the right page, or remove it. A
  `media.missing` item is a `/media/` file the store does not hold: find the
  file and attach it again, or remove the embed.
- **orphan:** link the page from the pages it relates to, or from a hub.
- **missing_hub:** create a `type: hub` page carrying the tag, linking every
  page in that area with a line on each.

When a research trail proposes a new tag, check `wiki_taxonomy` first: it
lists the tags that exist, their descriptions and the pages using each, and
any tags pages already use without a taxonomy entry. Only Patch edits
`taxonomy.yml`, so new tags go under "Proposed changes". Once Patch approves
one in conversation, apply it: `references/owner-approved-changes.md`.
- **unfolded_proposal:** read the trail's "Proposed changes". Apply the ones
  that are right; decline the rest with a reason, or pass them to Patch
  under your "Proposed changes". Then record the decision under your "Changes
  made" with a link to the trail (`[slug](/trails/<slug>.md)`). A link under
  "Changes made" is what marks a trail's proposals handled; links in other
  sections do not. It covers the proposals the trail held when you linked
  it: when a trail gains proposals later, the scan reports it again with only
  the new lines, and you decide on those.
- **stale:** a page past its volatility half-life. Recheck its key claims
  against its sources. If they still hold and you changed nothing, leave the
  page and note it. If it needs fresh research, `job_create` a research job
  naming the page, and list the job id.
- **oversize:** the scan counts prose only, and gives a page in three or more
  sections twice the limit, so an item means real prose to move. Split out the
  sections it names (or, for a page with little structure, the concepts) into
  linked pages, and leave the original as the overview. Before splitting a
  long unsectioned page, consider whether sections, a table or a diagram
  would serve better.
- **source:** a hash mismatch (`source.sha256_mismatch`,
  `source.media_sha256_mismatch`), a missing media file or a missing trail is
  for Patch, even when the capture looks empty; list it. For an uncited source, cite it where it
  supports a claim, or leave it.
- **unreviewed:** promote a page to `curated` (`wiki_edit` with a `frontmatter`
  patch, `curation: curated`, and `updated`) only when all of these hold:
  - every factual claim is cited, and each quoted span appears verbatim in the
    cited source (read the sources). Line breaks do not count: a quote that
    matches except where the source breaks a line is verbatim. A quote from a
    code comment may also leave out the comment markers, as long as every
    word is the source's own;
  - it is the only page for its concept, and its name follows the rule;
  - figures are attached files (not hotlinks), shown full size with a caption;
  - it links to the related pages that exist, and they link back. A page with
    no related pages yet still qualifies; say so in the trail;
  - `confidence` is set and matches the evidence;
  - claims about the world rest on external sources. A `type: agent-output`
    source (our own agents' output) supports only claims about the run that
    produced it.

  Otherwise fix what is missing or leave it unreviewed, with a note on why.
  Never set `verified`; only a human does.

Calibration runs (`type: calibration` trails) are read-only and their reports
are `agent-output` sources, so the scan reports neither their proposals nor
the pages only they changed, such as the calibration hub. To list the runs on
that hub, use `wiki_trails` with `kind: calibration`; its `listing_markdown`
is a ready table.

You may delete. Delete a page only when its content lives on elsewhere (a
merge) or it is plainly wrong or empty, and say why in the trail.

## 4. Record and finish

1. "Changes made": one line per change, with links to the pages touched and
   what was done (merged into, moved to, promoted, split, deleted).
2. "Proposed changes": what needs Patch, each with the reason.
3. Run `wiki_curation_scan` again and add the before and after counts per class
   under "Queries and methods".
4. `wiki_lint`: fix any findings your changes caused.
5. "Open questions and gaps", "None." under any empty section, and `finished`
   in the frontmatter.
6. Report the trail slug, the number of changes per class, and anything left
   for Patch.
