package facade

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/FuzzySlipper/den-wiki/internal/ledger"
	"github.com/FuzzySlipper/den-wiki/internal/media"
	"github.com/FuzzySlipper/den-wiki/internal/registry"
)

// Deps are the facade's collaborators. Wiki may be nil (no OpenKnowledge
// configured); its operations then answer backend_unavailable.
type Deps struct {
	Ledger     *ledger.Service
	Wiki       WikiBackend
	ContentDir string
	// Commits makes the content commits. Give the same one to RunUICommits
	// so UI commits and facade writes share a lock; nil makes a private one.
	Commits *Committer
	// Media is the media store wiki_attach puts video and very large files
	// in, served at /media/; nil means none. MediaGitMax is the size above
	// which a file goes there instead of git; zero means DefaultMediaGitMax.
	Media       *media.Store
	MediaGitMax int64
	// PublishURL is doc-publish's publications endpoint for wiki_publish;
	// empty disables it. PublishTimeout bounds one call; zero means
	// DefaultPublishTimeout.
	PublishURL     string
	PublishTimeout time.Duration
	Reads          ReadLog
	Logger         *slog.Logger
	Clock          func() time.Time
}

// Operations returns every den-wiki operation over deps. The MCP handler and
// the CLI catalog both come from this one list.
func Operations(d Deps) []*registry.Operation {
	var ops []*registry.Operation
	ops = append(ops, ledgerOps(d.Ledger)...)
	ops = append(ops, wikiOps(d)...)
	mediaDir := ""
	if d.Media != nil {
		mediaDir = d.Media.Dir
	}
	ops = append(ops, lintOps(d.ContentDir, mediaDir)...)
	if d.PublishTimeout <= 0 {
		d.PublishTimeout = DefaultPublishTimeout
	}
	ops = append(ops, publishOps(d)...)
	return ops
}

// NewRegistry builds the registry over deps.
func NewRegistry(d Deps) (*registry.Registry, error) { return registry.NewRegistry(Operations(d)...) }

// Version is the build's VCS revision, or "dev".
func Version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	rev, dirty := "", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if dirty {
		rev += "-dirty"
	}
	return rev
}

// NewServer returns the facade's HTTP handler: /mcp, the ledger's /v1/jobs
// API, /healthz, /version, /v1/export for shared pages (with a content dir)
// and, with a media store, /media/.
func NewServer(d Deps, reg *registry.Registry) http.Handler {
	if d.Clock == nil {
		d.Clock = time.Now
	}
	version := Version()
	mux := http.NewServeMux()
	mux.Handle("/mcp", NewMCP(reg, d.Reads, d.Logger, d.Clock, version))
	ledger.NewHandler(d.Ledger).RegisterRoutes(mux)
	if d.Media != nil {
		mux.Handle(media.URLPrefix, d.Media.Handler())
	}
	if d.ContentDir != "" {
		h := exportHandler(d.ContentDir)
		mux.Handle("GET /v1/export", h)
		mux.Handle("GET /v1/export/asset", h)
	}
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"service": "den-wiki", "commit": version, "catalog_revision": reg.Revision()})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		checks := map[string]string{"ledger": "ok", "openknowledge": "not_configured"}
		status := http.StatusOK
		if err := d.Ledger.Ping(ctx); err != nil {
			checks["ledger"], status = err.Error(), http.StatusServiceUnavailable
		}
		if d.Wiki != nil {
			if err := d.Wiki.Ping(ctx); err != nil {
				// OK down degrades wiki tools only; the ledger still works.
				checks["openknowledge"] = "unavailable: " + err.Error()
			} else {
				checks["openknowledge"] = "ok"
			}
		}
		writeJSON(w, status, map[string]any{"status": map[bool]string{true: "ok", false: "degraded"}[status == http.StatusOK], "checks": checks, "commit": version})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
