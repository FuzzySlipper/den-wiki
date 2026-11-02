package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/FuzzySlipper/den-wiki/internal/config"
	"github.com/FuzzySlipper/den-wiki/internal/facade"
	"github.com/FuzzySlipper/den-wiki/internal/ledger"
	"github.com/FuzzySlipper/den-wiki/internal/media"
	"github.com/FuzzySlipper/den-wiki/internal/okclient"
)

// leaseSweep is how often serve expires lapsed leases in the background.
// Every ledger call also expires them lazily.
const leaseSweep = 30 * time.Second

func runServe(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("den-wiki serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "server config file (YAML; see docs/facade.md)")
	fs.Usage = func() {
		fmt.Fprint(stderr, "Usage: den-wiki serve --config <file>\n\nRuns the den-wiki service: the MCP facade at /mcp, the ledger API at /v1/jobs,\n/healthz and /version, in one process.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if *cfgPath == "" || fs.NArg() > 0 {
		fs.Usage()
		return exitUsage
	}
	cfg, err := config.LoadServer(*cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "den-wiki serve: %v\n", err)
		return exitUsage
	}
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := serve(ctx, cfg, logger, nil); err != nil {
		logger.Error("serve_failed", "error", err.Error())
		return exitRuntime
	}
	return exitOK
}

// serve runs until ctx ends. When ready is non-nil it receives the bound
// address once the listener is up.
func serve(ctx context.Context, cfg config.Server, logger *slog.Logger, ready chan<- string) error {
	store, err := ledger.OpenSQLite(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer store.Close()
	reads, err := facade.OpenReadLog(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer reads.Close()
	var opts []ledger.Option
	if cfg.ContentDir != "" {
		opts = append(opts, ledger.WithContentDir(cfg.ContentDir))
	}
	svc := ledger.NewService(store, time.Now, opts...)
	commits := facade.NewCommitter(cfg.ContentDir)
	deps := facade.Deps{Ledger: svc, ContentDir: cfg.ContentDir, Commits: commits, Reads: reads, Logger: logger, Clock: time.Now, MediaGitMax: cfg.Media.GitMaxBytes, PublishURL: cfg.Publish.DocPublishURL, PublishTimeout: cfg.Publish.Timeout}
	if dir := cfg.Media.Dir; dir != "" {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return fmt.Errorf("media.dir %s is not a directory; create or mount it first", dir)
		}
		deps.Media = &media.Store{Dir: dir}
	}
	if cfg.OpenKnowledge.URL != "" {
		deps.Wiki = facade.NewOKWiki(okclient.New(cfg.OpenKnowledge.URL, cfg.OpenKnowledge.Timeout))
	}
	reg, err := facade.NewRegistry(deps)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: facade.NewServer(deps, reg), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		t := time.NewTicker(leaseSweep)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if n, err := svc.ExpireLeases(ctx); err != nil {
					logger.Warn("lease_sweep_failed", "error", err.Error())
				} else if n > 0 {
					logger.Info("leases_expired", "count", n)
				}
			}
		}
	}()
	if ui := cfg.UICommits; cfg.ContentDir != "" && !ui.Disabled {
		go commits.RunUICommits(ctx, facade.UIOptions{Author: ui.Author, Interval: ui.Interval, Quiet: ui.Quiet}, logger)
	}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	logger.Info("den_wiki_listening", "addr", ln.Addr().String(), "catalog_revision", reg.Revision(), "commit", facade.Version(), "openknowledge", cfg.OpenKnowledge.URL)
	if ready != nil {
		ready <- ln.Addr().String()
	}
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
