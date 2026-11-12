package okwatch

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeUnit struct {
	active   bool
	since    time.Time
	restarts int
}

func (u *fakeUnit) State(context.Context) (bool, time.Time, error) { return u.active, u.since, nil }

func (u *fakeUnit) Restart(context.Context) error {
	u.restarts++
	return nil
}

func TestWatchdogRestartsAfterConsecutiveFailures(t *testing.T) {
	now := time.Date(2026, 11, 11, 8, 30, 0, 0, time.UTC)
	unit := &fakeUnit{active: true, since: now.Add(-time.Hour)}
	var answering bool
	var logs bytes.Buffer
	w := &Watchdog{
		Ping: func(context.Context) error {
			if answering {
				return nil
			}
			return errors.New("context deadline exceeded")
		},
		Unit: unit, Failures: 3, Grace: 2 * time.Minute,
		Now: func() time.Time { return now }, Log: log.New(&logs, "", 0),
	}
	ctx := context.Background()

	// A failure that recovers before the threshold restarts nothing.
	w.Check(ctx)
	w.Check(ctx)
	answering = true
	w.Check(ctx)
	if unit.restarts != 0 || !strings.Contains(logs.String(), "answered again after 2 failed pings") {
		t.Fatalf("recovered: %d restarts\n%s", unit.restarts, logs.String())
	}

	// Three in a row restart it, once, and the count starts again.
	answering = false
	w.Check(ctx)
	w.Check(ctx)
	if unit.restarts != 0 {
		t.Fatal("restarted before the threshold")
	}
	w.Check(ctx)
	if unit.restarts != 1 || w.Restarts() != 1 || !strings.Contains(logs.String(), "restarting OpenKnowledge after 3 failed pings (restart 1") {
		t.Fatalf("hang: %d restarts\n%s", unit.restarts, logs.String())
	}

	// While the restarted unit is inside its grace period nothing is pinged
	// or counted.
	unit.since = now
	for range 5 {
		w.Check(ctx)
	}
	if unit.restarts != 1 {
		t.Errorf("restarted during grace: %d", unit.restarts)
	}

	// A stopped unit is systemd's business.
	unit.since, unit.active = now.Add(-time.Hour), false
	for range 5 {
		w.Check(ctx)
	}
	if unit.restarts != 1 {
		t.Errorf("restarted a stopped unit: %d", unit.restarts)
	}
}

func TestSystemdUnitReadsStateAndRestarts(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	fake := filepath.Join(dir, "systemctl")
	script := "#!/bin/sh\necho \"$*\" >> " + calls + "\n" +
		"[ \"$1\" = show ] && printf 'ActiveState=active\\nActiveEnterTimestamp=@1762850000\\n'\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	u := SystemdUnit{Name: "ok-wiki.service", Systemctl: fake}
	active, since, err := u.State(context.Background())
	if err != nil || !active || !since.Equal(time.Unix(1762850000, 0)) {
		t.Fatalf("state %v %v %v", active, since, err)
	}
	if err := u.Restart(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(calls)
	want := "show --timestamp=unix --property=ActiveState,ActiveEnterTimestamp ok-wiki.service\nrestart ok-wiki.service\n"
	if string(b) != want {
		t.Errorf("calls %q, want %q", b, want)
	}
}
