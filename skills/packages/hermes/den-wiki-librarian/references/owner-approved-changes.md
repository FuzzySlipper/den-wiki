# Changes Patch has approved

Some changes are Patch's call (new tags, taxonomy edits). Once Patch
approves one in conversation, the librarian applies it rather than leaving it
proposed.

## Adding a taxonomy tag or topic

`taxonomy.yml` is not a zone file, so `wiki_write`/`wiki_edit` cannot touch
it, and the content repo lives inside the den-wiki container (192.168.1.13,
`/srv/den-wiki-content`). The NAS copy is a push mirror: changing it would
only diverge. Edit it in the OpenKnowledge web editor. Nothing commits the
change for you: den-wiki's background commits of UI edits cover only
`sources/`, `trails/` and `wiki/`, so the edit stays on disk, uncommitted
and missing from the NAS backup, until someone commits it (step 5).

1. Browser: open `https://edit.wiki.dragonden.stream/#/taxonomy.yml`. Read the
   current text from `.cm-content .cm-line` elements to match the entry
   format (`- name:` / `description:` / optional `parent:`). Tags and topics
   have the same shape; topics are the `topics:` list.
2. Click on the visible text of the last entry's `description` line. The
   editor scrolls horizontally on long lines, so take coordinates from a
   screenshot, not from the element's bounding box, and confirm that
   `document.activeElement` is `cm-content`.
3. Press `End`, then `Input.insertText` with the entry starting with `\n`.
   insertText bypasses auto-indent, so the indentation typed is the
   indentation saved. Re-read the lines to check.
4. The editor autosaves. Confirm with `wiki_taxonomy` (the entry is listed)
   and `wiki_lint`, not with the editor. Both read the file on disk, so they
   show the change before it is committed.
5. Commit it on the den-wiki container, as `denwiki`, with your trail's tag:
   `git -C /srv/den-wiki-content commit -m "<what changed> (trail:<slug>)"
   -- taxonomy.yml`. Name the file so other pending edits stay out of the
   commit. If you have no shell on the container, say under "Proposed
   changes" in your trail, and in your run report, that `taxonomy.yml` has an
   uncommitted approved edit, so Patch or a session with container access
   commits it.
6. Tag or sort the pages with `wiki_edit` frontmatter patches (`tags` is
   replaced whole, so include existing tags; `topic` is one value) under a
   short curation trail (`type: curation`, `curation-<date>-<what>`)
   recording the taxonomy edit, its commit and each page.

A tag description can carry a framing Patch cares about. For example,
`geopolitics` covers "Wars, states and international relations, and the
domestic politics that drive them."

## Subpages and hidden pages

den-wiki has no subpage concept and no way to keep a page out of listings.
The UI is a file tree: a `wiki/<folder>/` shows as one collapsed entry, and
each folder gets a generated `index.md`. Dated run records (calibration runs,
experiments) are events. The contract keeps events out of `wiki/`, so they
belong in `trails/` (flat, append-only, required sections; extra `##`
sections are allowed), with a `meta` hub page in `wiki/` holding the current
conclusions and linking the runs.

Give such a trail its kind in `type` (`calibration` for a calibration run).
OpenKnowledge's generated `trails/index.md` groups trails by `type`, so each
kind gets its own section and does not bury research runs. For the hub's list
of runs, use `wiki_trails` with that `kind`: its `listing_markdown` is a
ready table. The curation scan does not count a page that only calibration
runs have changed, such as that hub, as review backlog.
