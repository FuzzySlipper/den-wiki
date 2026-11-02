package facade

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/FuzzySlipper/den-wiki/internal/media"
	"github.com/FuzzySlipper/den-wiki/internal/registry"
)

// MaxAttachBytes bounds one attached file. A call carries the file base64
// encoded, so the MCP body limit is set from it.
const MaxAttachBytes = 96 << 20

// DefaultMediaGitMax is the size above which an attached file goes to the
// media store instead of git, when the configuration does not say.
const DefaultMediaGitMax = 50 << 20

type wikiAttachArgs struct {
	Page          string `json:"page" required:"true" desc:"The page, source or trail the file belongs to, e.g. wiki/agent-memory.md. It need not exist yet. Files kept in git go in its <doc>.assets/ folder."`
	Name          string `json:"name" required:"true" desc:"File name: a lowercase slug with an accepted extension (png, jpg, jpeg, gif, webp, avif, svg, mp4, webm, pdf), e.g. strike-map.png."`
	ContentBase64 string `json:"content_base64" required:"true" desc:"The file, base64 encoded, at full size: never downscale or recompress. Up to 96 MiB. On the CLI, @<path> reads and encodes a local file."`
	Alt           string `json:"alt,omitempty" desc:"Alt text or link text for the returned embed snippet; default the name."`
	Store         string `json:"store,omitempty" enum:"auto,git,media" desc:"auto (default): video and files over the git limit go to the media store, the rest to git. media forces the media store; git refuses video and files over the limit."`
	Trail         string `json:"trail" required:"true" desc:"Slug of your run's trail (trails/<slug>.md)."`
	Summary       string `json:"summary,omitempty" desc:"Commit subject before the trail tag; default \"Attach <path>\"."`
}

var attachName = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*\.[a-z0-9]+$`)

func mediaOps(w *wiki) []*registry.Operation {
	return []*registry.Operation{
		registry.New(registry.Operation{
			Name: "wiki_attach", Risk: registry.RiskWrite, Tier: registry.TierDefault, Backend: "openknowledge",
			Description: "Attach an image, GIF, video clip or PDF to a page, source or trail, at full size. Images go into git beside the page (<doc>.assets/) and are committed with (trail:<slug>); video and very large files go to the content-addressed media store (/media/<sha256>.<ext>), outside git. Returns the sha256 and an embed snippet to paste into the page.",
			Examples: []registry.Example{{Description: "Attach a chart to a page", Arguments: map[string]any{
				"page": "wiki/agent-memory.md", "name": "recall-by-store.png", "content_base64": "<base64>", "alt": "Recall by memory store",
				"trail": "agent-memory-research-2026-10-28",
			}}},
		}, func(ctx context.Context, c *registry.Call, a wikiAttachArgs) (any, error) {
			return w.attach(ctx, c, a)
		}),
	}
}

func (w *wiki) attach(ctx context.Context, c *registry.Call, a wikiAttachArgs) (any, error) {
	if err := w.ready(); err != nil {
		return nil, err
	}
	rel, doc, err := docPath(a.Page)
	if err != nil {
		return nil, err
	}
	if err := checkTrail(a.Trail); err != nil {
		return nil, err
	}
	if !attachName.MatchString(a.Name) {
		return nil, badArg("name %q must be a lowercase slug with an extension, e.g. strike-map.png", a.Name)
	}
	t, ok := media.TypeOf(a.Name)
	if !ok {
		return nil, badArg("name %q has an extension den-wiki does not accept; use one of %s", a.Name, media.Extensions())
	}
	if base64.StdEncoding.DecodedLen(len(a.ContentBase64)) > MaxAttachBytes+3 {
		return nil, badArg("the file is over %d MiB; trim or re-encode the clip, or split it", MaxAttachBytes>>20)
	}
	data, err := base64.StdEncoding.DecodeString(a.ContentBase64)
	if err != nil {
		return nil, badArg("content_base64 is not standard base64: %v", err)
	}
	if len(data) == 0 {
		return nil, badArg("content_base64 is empty")
	}
	toMedia := t.Kind == media.Video || int64(len(data)) > w.gitMax
	switch a.Store {
	case "", "auto":
	case "media":
		toMedia = true
	case "git":
		if toMedia {
			return nil, badArg("%s cannot go into git: video and files over %d MiB go to the media store (store auto or media)", a.Name, w.gitMax>>20)
		}
	default:
		return nil, badArg("store must be auto, git or media")
	}
	alt := strings.TrimSpace(a.Alt)
	if alt == "" {
		alt = a.Name
	}
	sum := media.Hash(data)
	out := map[string]any{"page": rel, "name": a.Name, "mime": t.MIME, "bytes": len(data), "sha256": sum}

	if toMedia {
		if w.media == nil {
			return nil, &registry.Error{Code: registry.CodeBackendUnavailable, Message: "this den-wiki has no media store configured (media.dir), so video and files over the git limit cannot be attached"}
		}
		name, err := w.media.Put(t.Ext, data)
		if err != nil {
			return nil, &registry.Error{Code: registry.CodeBackendError, Message: err.Error()}
		}
		url := media.URLPrefix + name
		out["stored"], out["url"], out["embed"] = "media", url, embedSnippet(t, url, alt)
		return out, nil
	}

	// In git: through OpenKnowledge into the page's assets folder, then a
	// trail-tagged commit, as for any write.
	assetRel := strings.TrimSuffix(rel, ".md") + ".assets/" + a.Name
	w.git.writes.Lock()
	defer w.git.writes.Unlock()
	subj := subject(a.Summary, "Attach "+assetRel, a.Trail)
	res, err := w.call(ctx, c.Agent, "write", map[string]any{"asset": map[string]any{"path": doc + ".assets/" + a.Name, "content": a.ContentBase64}, "summary": subj})
	if err != nil {
		return nil, err
	}
	// OK keeps one copy of identical bytes: when it already holds them it
	// writes nothing and answers with the existing file's path.
	if asset, _ := res["asset"].(map[string]any); asset != nil {
		if p := str(asset, "path"); p != "" && p != assetRel {
			if checkContentPath(p) != nil {
				return nil, &registry.Error{Code: registry.CodeBackendError, Message: "OpenKnowledge stored the file at an unexpected path " + p}
			}
			assetRel = p
			out["deduplicated"] = true
		}
	}
	werr := w.git.waitFor(ctx, func() bool {
		b, ok := w.git.read(assetRel)
		return ok && bytes.Equal(b, data)
	})
	cm, cerr := w.git.settle(ctx, werr, c.Agent, subj, []string{assetRel})
	out["stored"], out["path"], out["commit"] = "git", assetRel, cm
	out["embed"] = embedSnippet(t, relativeTo(path.Dir(rel), assetRel), alt)
	if cerr != nil {
		out["commit_error"] = cerr.Error()
	}
	return out, nil
}

// relativeTo returns the link from a document in dir to target, both
// content-root-relative, in the ./ or ../ form the content contract uses.
func relativeTo(dir, target string) string {
	from := strings.Split(dir, "/")
	to := strings.Split(target, "/")
	i := 0
	for i < len(from) && i < len(to)-1 && from[i] == to[i] {
		i++
	}
	up := strings.Repeat("../", len(from)-i)
	if up == "" {
		up = "./"
	}
	return up + strings.Join(to[i:], "/")
}

// embedSnippet is the Markdown or JSX that shows a file in OpenKnowledge,
// in the forms the content contract lists.
func embedSnippet(t media.Type, src, alt string) string {
	alt = strings.NewReplacer("[", "", "]", "", "\"", "'", "\n", " ").Replace(alt)
	switch t.Kind {
	case media.Video:
		return fmt.Sprintf("<video src=\"%s\" controls preload=\"metadata\" title=\"%s\" />", src, alt)
	case media.Image:
		return fmt.Sprintf("![%s](%s)", alt, src)
	default:
		return fmt.Sprintf("[%s](%s)", alt, src)
	}
}

// storeRef returns the media store name an OK link target or href refers
// to: OK resolves /media/<name> to the content path media/<name>.
func storeRef(target string) (string, bool) {
	m := media.RefRe.FindStringSubmatch("/" + strings.TrimPrefix(target, "/"))
	if m == nil || !strings.HasPrefix(strings.TrimPrefix(target, "/"), "media/") {
		return "", false
	}
	return m[1], true
}

// targetHeld reports whether a link OK reported broken points at a file that
// exists: in the media store, or on disk (OK resolves a file target to its
// content-root-relative path; a document target has no extension).
func (w *wiki) targetHeld(resolved, href string) bool {
	if w.mediaHeld(resolved) || w.mediaHeld(href) {
		return true
	}
	return path.Ext(resolved) != "" && checkContentPath(resolved) == nil && w.git.exists(resolved)
}

// mediaHeld reports whether a link target is a media store reference the
// store holds. OK knows nothing of the store and calls such links dead.
func (w *wiki) mediaHeld(target string) bool {
	name, ok := storeRef(target)
	return ok && w.media != nil && w.media.Has(name)
}
