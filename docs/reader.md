# den-wiki reader

`den-wiki serve` serves a read-only reader for the wiki at every path the
facade does not claim, so `https://wiki.dragonden.stream/` opens it.
OpenKnowledge (OK) stays the editor, on its own host
(`https://edit.wiki.dragonden.stream`), and each page links to itself there.
The reader never writes. The design and its reasons are in the Den document
`den-wiki/den-wiki-design`, section Reader (#10558).

## Layout

There are three panes. Below 900px wide, the side panes become drawers
opened from the header.

- **Left: the topic tree.** It shows `taxonomy.yml`'s topics with page
  counts. Expanding a topic lists its pages, hubs first. Pages with no topic,
  or with a topic the taxonomy does not list, are under Unsorted.
- **Topic groups.** A topic with an `overview` page (content contract,
  Topics) is drawn as that page: its title links to the overview, and a
  separate toggle button expands the topic's subtopics and other pages. The
  overview is not listed again among them. The group opens when one of its
  pages, or the overview, is the page being read; the page is highlighted, or
  the group title when it is the overview. Breadcrumbs pass through the group
  to the overview, and on the overview end at the group's parent. The topic's
  own `/topic/<name>` listing still works and links the overview. An overview
  that is missing, or whose page does not name the topic, is ignored: the
  topic is drawn plainly and the page stays where its own topic puts it.
- **Centre: the page.** It has breadcrumbs from its topic, the title, type,
  review state, updated date and the edit link, then the body. Citation
  footnotes are gathered under References at the end. Each reference links to
  the captured source in the reader and, as "original", to the source's
  `source_url`.
- **Right: about the page.** It shows:
  - the contents (`##` and `###` headings, with the one being read
    highlighted);
  - the frontmatter (type, topic, tags, confidence, curation, volatility,
    verified, updated, citations, aliases and contradicts);
  - "What links here";
  - the page's git history, with each trail linked.

## Routes

| Route | Shows |
|---|---|
| `/` | The topics with their subtopics and counts, the page count, and recently updated pages |
| `/topic/<name>`, `/unsorted` | A topic's description, its overview page if it has one, subtopics and pages, each with its first paragraph |
| `/wiki/<page>`, `/sources/<path>`, `/trails/<slug>` | A content file: the path without `.md` (`.md` URLs redirect there) |
| `/wiki/<doc>.assets/<file>` and the like | A file a page shows, from the content dir: only the types `wiki_attach` accepts, with the media store's sandboxing CSP |
| `/search?q=` | OK search, wiki pages first |
| `/static/` | The reader's CSS and JavaScript, embedded in the binary |

## Rendering

Pages are rendered on the server (`internal/reader`) with goldmark and
`html/template`. Every construct in the content contract's "Supported
formatting" renders. The test renders `internal/facade/testdata/formatting-reference.md`
and checks each construct's HTML. The constructs render as follows:

- Callouts become styled boxes.
- `<details>` works natively.
- `mermaid` fences are drawn by Mermaid.
- `$…$` and `$$` are typeset by KaTeX. A `$` never pairs across a code span,
  and a closing `$` followed by a digit does not close, so prices stay text.
- `<img width={N} />` and `<video … />` are fixed up for HTML.
- A link to a wiki page that does not exist is red, and opens a page offering
  to search for it or create it in OK.

Raw HTML passes through only for `img`, `video`, `details`, `summary`, `br`,
`sub`, `sup` and `kbd`, with safe attributes. Any other HTML is shown as
text, and the reader's Content-Security-Policy allows only its own scripts.
Mermaid and KaTeX are vendored at pinned versions in
`internal/reader/static/vendor` (see its README). They load only on pages
that use them.

## Data

- **Page text, frontmatter and the topic tree** come from the content dir.
  Renders are cached until a file's modification time or size changes. The
  tree is rebuilt when `taxonomy.yml` or a page changes, checked at most every
  two seconds.
- **Backlinks and history** come from OK's `exec cat` briefing, the same one
  `wiki_read` uses, under the OK session `reader`. They are cached for a
  minute per page.
- **Search** is OK `search`.

If OK is down or not configured, pages still render. The backlinks, history
and search panels then say they are unavailable.

## Configuration

```yaml
reader:
  disabled: false          # the reader is on whenever content_dir is set
  site_title: den-wiki
  edit_url: https://edit.wiki.dragonden.stream   # empty hides "Edit in OpenKnowledge"
```

A page's edit link is `edit_url + "/#/" + <doc>`. This is OK's route for a
document: the path without `.md`.
