package facade

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/FuzzySlipper/den-wiki/internal/export"
	"github.com/FuzzySlipper/den-wiki/internal/registry"
)

// DefaultPublishTimeout bounds a doc-publish call when Deps does not say.
const DefaultPublishTimeout = 60 * time.Second

type wikiExportArgs struct {
	Path      string `json:"path" required:"true" desc:"The wiki page, e.g. wiki/agent-memory.md. Only wiki/ pages can be shared."`
	AssetBase string `json:"asset_base,omitempty" desc:"URL prefix for the page's images in the Markdown; default the bare file names."`
}

type wikiPublishArgs struct {
	Path    string   `json:"path" required:"true" desc:"The wiki page to share, e.g. wiki/agent-memory.md. Only that page is shared, never the pages it links to."`
	Publish bool     `json:"publish,omitempty" desc:"Publish the post. Default false previews it without writing the blog: preview first and show Patch the result."`
	Title   string   `json:"title,omitempty" desc:"Post title; default the page title."`
	Slug    string   `json:"slug,omitempty" desc:"Post URL slug; default from the title."`
	Tags    []string `json:"tags,omitempty" desc:"Post tags."`
}

// exportError maps an export error to a registry error.
func exportError(err error) error {
	switch {
	case errors.Is(err, export.ErrInvalid):
		return &registry.Error{Code: registry.CodeInvalidArguments, Message: err.Error()}
	case errors.Is(err, export.ErrNotFound):
		return &registry.Error{Code: registry.CodeNotFound, Message: err.Error()}
	}
	return &registry.Error{Code: registry.CodeBackendError, Message: fmt.Sprintf("export: %v", err)}
}

func publishOps(d Deps) []*registry.Operation {
	contentDir := d.ContentDir
	client := &http.Client{Timeout: d.PublishTimeout}
	return []*registry.Operation{
		registry.New(registry.Operation{
			Name: "wiki_export", Risk: registry.RiskRead, Backend: "export",
			Description: "Render one wiki page for readers outside the LAN, as wiki_publish shares it: Markdown without frontmatter, citations linked to the original sources, links to other wiki pages as plain text, and the images it shows listed as assets. Warnings name what is left out (video) or may not render (Mermaid, math).",
		}, func(_ context.Context, _ *registry.Call, a wikiExportArgs) (any, error) {
			if contentDir == "" {
				return nil, &registry.Error{Code: registry.CodeBackendUnavailable, Message: "the facade has no content_dir configured, so pages cannot be exported"}
			}
			p, err := export.Export(contentDir, strings.TrimPrefix(a.Path, "/"), a.AssetBase)
			if err != nil {
				return nil, exportError(err)
			}
			return p, nil
		}),
		registry.New(registry.Operation{
			Name: "wiki_publish", Risk: registry.RiskWrite, Backend: "doc-publish",
			Description: "Share one wiki page outside the LAN as a post on Patch's blog, through den-services doc-publish (the same blog as Den's 'share via blog'). Only that page goes, with its images; links to other wiki pages become plain text and citations link to the original sources. Previews by default; publish only when Patch asks, with publish true. Publishing the same page again updates its post.",
			Examples: []registry.Example{
				{Description: "Preview the post", Arguments: map[string]any{"path": "wiki/agent-memory.md"}},
				{Description: "Publish it", Arguments: map[string]any{"path": "wiki/agent-memory.md", "publish": true}},
			},
		}, func(ctx context.Context, c *registry.Call, a wikiPublishArgs) (any, error) {
			if d.PublishURL == "" {
				return nil, &registry.Error{Code: registry.CodeBackendUnavailable, Message: "this den-wiki has no publish.doc_publish_url configured, so pages cannot be shared"}
			}
			if contentDir == "" {
				return nil, &registry.Error{Code: registry.CodeBackendUnavailable, Message: "the facade has no content_dir configured, so pages cannot be exported"}
			}
			rel := strings.TrimPrefix(a.Path, "/")
			// Check the page here, so a bad path fails before doc-publish
			// fetches it back from /v1/export.
			if _, err := export.Export(contentDir, rel, ""); err != nil {
				return nil, exportError(err)
			}
			return docPublish(ctx, client, d.PublishURL, c.Agent, rel, a)
		}),
	}
}

// docPublish asks doc-publish to preview or publish a wiki page. doc-publish
// fetches the page from this den-wiki's /v1/export itself.
func docPublish(ctx context.Context, client *http.Client, base, agent, rel string, a wikiPublishArgs) (any, error) {
	type options struct {
		Title string   `json:"title,omitempty"`
		Slug  string   `json:"slug,omitempty"`
		Tags  []string `json:"tags,omitempty"`
	}
	body, err := json.Marshal(map[string]any{
		"source":       map[string]string{"wiki_page": rel},
		"options":      options{Title: a.Title, Slug: a.Slug, Tags: a.Tags},
		"requested_by": agent,
	})
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(base, "/")
	if !a.Publish {
		url += "/preview"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, &registry.Error{Code: registry.CodeBackendUnavailable, Message: fmt.Sprintf("doc-publish: %v", err)}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, &registry.Error{Code: registry.CodeBackendError, Message: fmt.Sprintf("doc-publish: reading response: %v", err)}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		msg := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
			msg = e.Error.Code + ": " + e.Error.Message
		}
		code := registry.CodeBackendError
		if resp.StatusCode == http.StatusConflict {
			code = registry.CodeConflict
		}
		return nil, &registry.Error{Code: code, Message: fmt.Sprintf("doc-publish answered %d: %s", resp.StatusCode, msg)}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, &registry.Error{Code: registry.CodeBackendError, Message: fmt.Sprintf("doc-publish: decoding response: %v", err)}
	}
	return out, nil
}

// exportHandler serves GET /v1/export and /v1/export/asset, which doc-publish
// fetches a shared page from.
func exportHandler(contentDir string) http.Handler {
	mux := http.NewServeMux()
	fail := func(w http.ResponseWriter, err error) {
		status, code := http.StatusInternalServerError, registry.CodeBackendError
		switch {
		case errors.Is(err, export.ErrInvalid):
			status, code = http.StatusBadRequest, registry.CodeInvalidArguments
		case errors.Is(err, export.ErrNotFound):
			status, code = http.StatusNotFound, registry.CodeNotFound
		}
		writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": err.Error()}})
	}
	mux.HandleFunc("GET /v1/export", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		p, err := export.Export(contentDir, q.Get("page"), q.Get("asset_base"))
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, p)
	})
	mux.HandleFunc("GET /v1/export/asset", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		a, b, err := export.ReadAsset(contentDir, q.Get("page"), q.Get("name"))
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", a.ContentType)
		w.Header().Set("X-Content-SHA256", a.SHA256)
		w.Header().Set("Content-Length", fmt.Sprint(len(b)))
		_, _ = w.Write(b)
	})
	return mux
}
