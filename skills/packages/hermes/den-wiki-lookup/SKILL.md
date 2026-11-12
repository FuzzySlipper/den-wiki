---
name: den-wiki-lookup
description: Look something up in den-wiki, the shared LAN research wiki (https://wiki.dragonden.stream), without doing research. Use when you need what the wiki already knows about a topic, a page's sources or history, or whether earlier research covered something. Read-only. To research and write into the wiki, use den-wiki-research.
---

# den-wiki lookup

Read-only use of den-wiki. Every operation is a tool on the `den-wiki` MCP
server and a command of the `den-wiki` CLI (`den-wiki <op> --args-json
'{...}'`). You are running in harness `hermes`.

1. `wiki_search` the topic's terms and synonyms (`titles_only: true` for a
   fast pass). Hits under `wiki/` are current understanding; hits under
   `trails/` are earlier research runs.
2. `wiki_read` the best hits with `content_only: true` for just the Markdown.
   A full `wiki_read` also carries the page's backlinks, outgoing links and git
   history, which can be larger than the page: use it when you need them. Each commit subject names the
   trail behind the change as `(trail:<slug>)`; read the trail for how a claim
   was reached and which sources were rejected.
3. Follow links with `wiki_links` and history with `wiki_history` when the
   page alone is not enough.
4. Weigh what you read: `curation: unreviewed` pages are agent-written and not
   yet curated, and `confidence` and the cited sources say how far to trust a
   claim.

Answer with page paths, so the reader can open them. If the wiki does not
cover what is needed, say so. Researching it is a separate run with the
den-wiki-research skill; `job_list` shows whether a job for it is already
queued.

Do not call `wiki_write`, `wiki_edit`, `wiki_move`, `wiki_delete` or any
`job_*` operation that changes a job from this skill.

Sharing a page outside the LAN is the one exception, and only when Patch asks
for it: `wiki_publish` (long tail) posts that one page to his blog through
den-services doc-publish, with its images, citations linked to the original
sources and links to other wiki pages as plain text. Call it without
`publish` first, show Patch the preview's post URL and warnings, and call it
again with `publish: true` only when he confirms. Never use OpenKnowledge's
Share button or share links for this.
