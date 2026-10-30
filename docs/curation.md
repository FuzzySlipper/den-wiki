# Curation

Writes to den-wiki are permissive; curation corrects after the fact. A
librarian agent runs a curation pass on a schedule. It scans, acts on what is
clearly right, proposes the rest to the owner, and records every change in
its own trail (`trails/curation-<date>-<what>.md`), whose slug tags its
commits. The owner reviews a pass by reading that trail, and rolls back any
change with git.

## The scan

`wiki_curation_scan` (MCP, long tail; `den-wiki wiki_curation_scan` on the
CLI) reports curation work without changing anything. The logic is
`lint.Curate` (`internal/lint/curate.go`). It reads the content directory, and
takes dead links and orphans from OpenKnowledge's link views. It returns:

- `items`: one per finding, with `class`, `path`, `related` paths, a `score`
  (higher is more urgent within the class) and a `message` naming the fix;
- `counts` per class, and `pages`, `trails` and `sources` totals;
- `summary_markdown`: a table of counts and the items by class, for the
  curation trail.

| Class | Finds | Score |
|---|---|---|
| `duplicate` | Two `wiki/` pages whose names are the same or nearly so. A page's names are its file name, title and aliases, reduced to word sets (lower case, stop words dropped, plurals folded); a pair counts when any two names reach Jaccard similarity 0.6 | The similarity |
| `naming` | `naming.*` lint findings: names with dates, hashes, versions or status words | 1 |
| `dead_link` | Links from pages and trails to missing pages (OpenKnowledge). Captured sources keep their original relative links, so their dead links are not reported | 1 |
| `orphan` | `wiki/` pages nothing links to (OpenKnowledge). Trails and sources are reached through page frontmatter, so they are not reported | 1 |
| `missing_hub` | A tag on 5 or more pages with no `type: hub` page carrying it; `path` is `tag:<name>` | Pages with the tag |
| `unfolded_proposal` | A research trail whose "Proposed changes" holds more than "None." and that no curation trail links to | 1 |
| `stale` | A page whose later of `updated` and `verified` is older than its volatility half-life: hot 30 days, warm 180 (also when `volatility` is missing), cold 730 | Age divided by half-life |
| `oversize` | A page body over 2500 words | Words divided by the limit |
| `source` | `source.sha256_mismatch`, `source.supersedes_missing` and missing `captured_by_trail` findings; and sources no page cites (in `sources` or a body link) and none supersedes | 1, or 0.5 for uncited |
| `unreviewed` | Pages still `curation: unreviewed` | Days since `updated` |

`oversize_words` and `hub_min_pages` override the two thresholds. Class names
are stable; a changed meaning gets a new class.

A curation trail "handles" a research trail by linking to it. The librarian
links every trail whose proposals it folded or declined, so the next scan
stops reporting them.

## The librarian

The procedure is the `den-wiki-librarian` skill ([skills](skills.md)). It is a
separate skill rather than a section of `den-wiki-research`: only the
librarian needs it, and research agents keep a shorter skill. In brief:

1. Write the curation trail first.
2. Scan, and read the last curation trails so declined proposals are not
   redone.
3. Work through the classes in order, about 15 changes a pass: merge
   duplicates (fold the content, repoint backlinks, delete), move misnamed
   pages, fix dead links and orphans, add hubs, fold or decline proposals,
   recheck stale pages or queue research jobs for them, split oversize pages,
   and promote pages to `curated` when every claim is cited verbatim and the
   page is linked and unique.
4. List every change, propose what needs the owner, rescan, and record the
   before and after counts.

The librarian may delete pages. It never sets `verified`, which is for
humans.

## Schedule

The librarian is a full Hermes profile, `librarian`, on den-k8 (user
`hermes-mj`). The owner can open it and talk to it, and see and change its
model, which starts strong (`claude-opus-5-5`) and can be stepped down as its
passes prove reliable. A Hermes cron job in that profile runs the nightly
pass. Cron sessions are ephemeral, but they use the profile's model, skills
and MCP servers, and the profile's main sessions can read their output.

| | |
|---|---|
| Profile | `librarian` (`~hermes-mj/.hermes/profiles/librarian`) |
| Agent identity | `hermes-librarian` (`X-Den-Wiki-Agent` in the profile's `mcp_servers.den-wiki`) |
| Skills | `den-wiki-librarian`, `den-wiki-research`, `den-wiki-lookup`, in the profile's `skills/research/` |
| Cron job | `den-wiki curation`, daily at 03:30 local time |
