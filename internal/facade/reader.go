package facade

import (
	"context"
	"errors"
	"strings"

	"github.com/FuzzySlipper/den-wiki/internal/reader"
)

// readerIdentity is the OK session the reader's lookups use.
const readerIdentity = "reader"

// NewReaderBackend gives the reader OK's backlinks, history and search, with
// the same noise filtered as the wiki tools.
func NewReaderBackend(ok WikiBackend) reader.Backend {
	return readerBackend{w: &wiki{ok: ok}}
}

type readerBackend struct{ w *wiki }

func (b readerBackend) Briefing(ctx context.Context, rel string) (reader.Briefing, error) {
	if !safePath.MatchString(rel) {
		return reader.Briefing{}, errors.New("OK's exec takes only plain paths, not " + rel)
	}
	res, err := b.w.call(ctx, readerIdentity, "exec", map[string]any{"command": "cat " + rel})
	if err != nil {
		return reader.Briefing{}, err
	}
	var out reader.Briefing
	eps := listOf(res["enrichedPaths"])
	if len(eps) == 0 {
		return out, nil
	}
	for _, l := range listOf(eps[0]["backlinks"]) {
		src := str(l, "source")
		if src == "" || noise(src) {
			continue
		}
		out.Backlinks = append(out.Backlinks, reader.Backlink{Path: src + ".md", Title: str(l, "title")})
	}
	for _, h := range listOf(eps[0]["projectHistory"]) {
		c := reader.Change{Author: str(h, "authorName"), Subject: str(h, "subject"), Commit: str(h, "hash"), Date: str(h, "date")}
		if len(c.Date) > 10 {
			c.Date = c.Date[:10]
		}
		if m := reader.TrailRe.FindStringSubmatch(c.Subject); m != nil {
			c.Trail = m[1]
		}
		out.History = append(out.History, c)
	}
	return out, nil
}

func (b readerBackend) Search(ctx context.Context, query string, limit int) ([]reader.Hit, error) {
	res, err := b.w.call(ctx, readerIdentity, "search", map[string]any{"query": query, "limit": limit})
	if err != nil {
		return nil, err
	}
	var out []reader.Hit
	for _, r := range listOf(res["results"]) {
		if str(r, "kind") != "page" {
			continue
		}
		doc := strings.TrimSuffix(str(r, "path"), ".md")
		if doc == "" {
			doc = str(r, "docName")
		}
		if doc == "" || noise(doc) {
			continue
		}
		out = append(out, reader.Hit{Path: doc + ".md", Title: str(r, "title"), Snippet: str(r, "snippet")})
	}
	return out, nil
}
