// Package okwatch restarts OpenKnowledge when it stops answering.
//
// OK can wedge with its systemd unit still active: on 2026-11-11 its file
// watcher lost its subscription under a bulk import and the server stopped
// answering MCP for 35 minutes until someone restarted it (#10740). systemd's
// Restart=on-failure only covers a process that exits, so den-wiki runs this
// watchdog beside it: it pings OK's MCP endpoint every interval and restarts
// the unit after a run of failed pings, logging every failure and restart.
package okwatch

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Unit is the systemd unit that runs OK.
type Unit interface {
	// State reports whether the unit is active and since when.
	State(ctx context.Context) (active bool, since time.Time, err error)
	Restart(ctx context.Context) error
}

// Watchdog pings OK and restarts its unit after Failures pings in a row fail.
type Watchdog struct {
	Ping     func(ctx context.Context) error
	Unit     Unit
	Failures int           // consecutive failed pings that trigger a restart
	Grace    time.Duration // no pings until the unit has been active this long
	Now      func() time.Time
	Log      *log.Logger

	failed   int
	restarts int
}

// Restarts is how many restarts this watchdog has made.
func (w *Watchdog) Restarts() int { return w.restarts }

// Check runs one round: it reads the unit's state, pings OK and restarts the
// unit when this ping makes Failures in a row. A unit that is not active, or
// has only just started, is left to systemd and resets the count.
func (w *Watchdog) Check(ctx context.Context) {
	active, since, err := w.Unit.State(ctx)
	if err != nil {
		w.Log.Printf("cannot read the unit's state: %v", err)
		return
	}
	if !active || w.Now().Sub(since) < w.Grace {
		w.failed = 0
		return
	}
	if err := w.Ping(ctx); err != nil {
		w.failed++
		w.Log.Printf("OpenKnowledge did not answer (%d of %d): %v", w.failed, w.Failures, err)
		if w.failed < w.Failures {
			return
		}
		w.failed = 0
		w.restarts++
		w.Log.Printf("restarting OpenKnowledge after %d failed pings (restart %d since the watchdog started)", w.Failures, w.restarts)
		if err := w.Unit.Restart(ctx); err != nil {
			w.Log.Printf("restart failed: %v", err)
		}
		return
	}
	if w.failed > 0 {
		w.Log.Printf("OpenKnowledge answered again after %d failed pings", w.failed)
		w.failed = 0
	}
}

// Run checks every interval until ctx ends.
func (w *Watchdog) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		w.Check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// SystemdUnit drives a unit through systemctl.
type SystemdUnit struct {
	Name      string
	Systemctl string // the systemctl binary; default "systemctl"
}

func (u SystemdUnit) systemctl(ctx context.Context, args ...string) ([]byte, error) {
	bin := u.Systemctl
	if bin == "" {
		bin = "systemctl"
	}
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w: %s", bin, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// State reads ActiveState and ActiveEnterTimestamp.
func (u SystemdUnit) State(ctx context.Context) (bool, time.Time, error) {
	out, err := u.systemctl(ctx, "show", "--timestamp=unix", "--property=ActiveState,ActiveEnterTimestamp", u.Name)
	if err != nil {
		return false, time.Time{}, err
	}
	var active bool
	var since time.Time
	for _, line := range strings.Split(string(out), "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch k {
		case "ActiveState":
			active = v == "active"
		case "ActiveEnterTimestamp":
			if sec, err := strconv.ParseInt(strings.TrimPrefix(v, "@"), 10, 64); err == nil {
				since = time.Unix(sec, 0)
			}
		}
	}
	return active, since, nil
}

// Restart restarts the unit.
func (u SystemdUnit) Restart(ctx context.Context) error {
	_, err := u.systemctl(ctx, "restart", u.Name)
	return err
}
