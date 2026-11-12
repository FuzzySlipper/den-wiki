package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/FuzzySlipper/den-wiki/internal/okclient"
	"github.com/FuzzySlipper/den-wiki/internal/okwatch"
)

func runWatchdog(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("den-wiki ok-watchdog", flag.ContinueOnError)
	fs.SetOutput(stderr)
	url := fs.String("url", "http://127.0.0.1:4800/mcp", "OpenKnowledge's MCP endpoint")
	unit := fs.String("unit", "ok-wiki.service", "the systemd unit that runs OpenKnowledge")
	interval := fs.Duration("interval", time.Minute, "time between pings")
	timeout := fs.Duration("timeout", 15*time.Second, "how long one ping may take")
	failures := fs.Int("failures", 3, "failed pings in a row that restart the unit")
	grace := fs.Duration("grace", 2*time.Minute, "no pings until the unit has been active this long")
	user := fs.Bool("user", false, "the unit is in the user's service manager (systemctl --user), for trying the watchdog outside the container")
	fs.Usage = func() {
		fmt.Fprint(stderr, "Usage: den-wiki ok-watchdog [flags]\n\nPings OpenKnowledge's MCP endpoint every interval and restarts its systemd\nunit after a run of failed pings, for an OK that hangs with its unit still\nactive. Every failure and restart is logged. Runs until stopped; needs the\nright to restart the unit.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() > 0 || *interval <= 0 || *timeout <= 0 || *failures < 1 || *grace < 0 {
		fs.Usage()
		return exitUsage
	}
	ok := okclient.New(*url, *timeout)
	w := &okwatch.Watchdog{
		Ping: func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, *timeout)
			defer cancel()
			return ok.Ping(ctx)
		},
		Unit:     okwatch.SystemdUnit{Name: *unit, User: *user},
		Failures: *failures,
		Grace:    *grace,
		Now:      time.Now,
		Log:      log.New(stderr, "ok-watchdog: ", 0),
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	w.Log.Printf("watching %s at %s: restart after %d failed pings, one every %s", *unit, *url, *failures, *interval)
	w.Run(ctx, *interval)
	return exitOK
}
