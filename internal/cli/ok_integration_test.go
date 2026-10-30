//go:build okintegration

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/FuzzySlipper/den-wiki/internal/facade"
	"github.com/FuzzySlipper/den-wiki/internal/ledger"
	"github.com/FuzzySlipper/den-wiki/internal/okclient"
	"github.com/FuzzySlipper/den-wiki/internal/testok"
)

const cliTrail = `---
type: trail
question: Can every operation be driven from the CLI?
started: 2026-10-29T14:00:00Z
agent: cli-agent
harness: claude-code
---

## Question and scope

CLI parity probe.

## Prior coverage checked

None.

## Queries and methods

None.

## Sources

None.

## Dead ends

None.

## Changes made

None.

## Proposed changes

None.

## Open questions and gaps

None.
`

const cliPage = `---
type: concept
title: CLI probe page
aliases: []
tags: [meta]
curation: unreviewed
updated: 2026-10-29
---

# CLI probe page

Written through the den-wiki CLI.
`

// Every catalog operation runs through the CLI, over HTTP MCP, against the
// facade backed by a real OpenKnowledge and ledger.
func TestEveryOperationThroughTheCLI(t *testing.T) {
	stack := testok.Start(t, "../..")
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "den-wiki.db")
	store, err := ledger.OpenSQLite(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	reads, err := facade.OpenReadLog(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reads.Close() })
	d := facade.Deps{
		Ledger: ledger.NewService(store, time.Now, ledger.WithContentDir(stack.ContentDir)),
		Wiki:   facade.NewOKWiki(okclient.New(stack.MCP, 30*time.Second)), ContentDir: stack.ContentDir,
		Reads: reads, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), Clock: time.Now,
	}
	reg, err := facade.NewRegistry(d)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(facade.NewServer(d, reg))
	t.Cleanup(srv.Close)
	endpoint := srv.URL + "/mcp"
	cfg := filepath.Join(t.TempDir(), "cli.yml")
	if err := os.WriteFile(cfg, []byte("endpoint: "+endpoint+"\nagent: cli-agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	argsFile := func(v map[string]any) string {
		raw, _ := json.Marshal(v)
		f, _ := os.CreateTemp(dir, "args-*.json")
		_, _ = f.Write(raw)
		_ = f.Close()
		return "@" + f.Name()
	}

	called := map[string]bool{}
	run := func(want int, args ...string) map[string]any {
		t.Helper()
		var out, errb bytes.Buffer
		code := Run(append(args, "--config", cfg), Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb})
		if code != want {
			t.Fatalf("den-wiki %s: exit %d, want %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, want, out.String(), errb.String())
		}
		called[args[0]] = true
		var res map[string]any
		_ = json.Unmarshal(out.Bytes(), &res)
		return res
	}
	id := func(m map[string]any) string { raw, _ := json.Marshal(m["id"]); return string(raw) }

	// Reads.
	run(ExitOK, "wiki_lint_rules")
	run(ExitOK, "wiki_lint", "--min-severity", "warning")
	if r := run(ExitOK, "wiki_search", "--query", "editable memory", "--limit", "5"); !strings.Contains(mustJSON(r), "wiki/agent-memory.md") {
		t.Fatalf("search: %v", r)
	}
	run(ExitOK, "wiki_read", "--path", "wiki/agent-memory.md")
	run(ExitOK, "wiki_list", "--folder", "wiki")
	run(ExitOK, "wiki_links", "--kinds", "dead,orphans,hubs")
	run(ExitOK, "wiki_history", "--path", "wiki/agent-memory.md", "--limit", "5")
	run(ExitOK, "wiki_audit", "--path", "wiki")

	// Writes, with large content through --args-json @file.
	run(ExitOK, "wiki_write", "--args-json", argsFile(map[string]any{"path": "trails/cli-probe.md", "content": cliTrail, "trail": "cli-probe", "summary": "Start CLI probe"}))
	w := run(ExitOK, "wiki_write", "--args-json", argsFile(map[string]any{"path": "wiki/cli-probe-page.md", "content": cliPage, "trail": "cli-probe"}))
	if w["commit"] == nil {
		t.Fatalf("write made no commit: %v", w)
	}
	run(ExitOK, "wiki_edit", "--path", "wiki/cli-probe-page.md", "--find", "through the den-wiki CLI", "--replace", "through the den-wiki CLI, unaided", "--trail", "cli-probe")
	run(ExitOK, "wiki_move", "--from", "wiki/cli-probe-page.md", "--to", "wiki/ai/cli-probe-page.md", "--trail", "cli-probe")
	run(ExitOK, "wiki_delete", "--path", "wiki/ai/cli-probe-page.md", "--trail", "cli-probe", "--summary", "Remove CLI probe page")
	if log := stack.Git(t, "log", "--format=%an|%s", "-1"); !strings.Contains(log, "cli-agent|Remove CLI probe page (trail:cli-probe)") {
		t.Fatalf("last commit: %s", log)
	}

	// Ledger.
	job := run(ExitOK, "job_create", "--question", "Is the CLI complete?", "--priority", "2")
	run(ExitOK, "job_get", "--id", id(job))
	run(ExitOK, "job_list", "--statuses", "open", "--limit", "10")
	run(ExitOK, "job_edit", "--id", id(job), "--scope-notes", "CLI only")
	claim := run(ExitOK, "job_claim", "--id", id(job), "--harness", "claude-code", "--lease-seconds", "120")
	token := claim["lease_token"].(string)
	run(ExitConflict, "job_claim", "--id", id(job), "--agent", "someone-else")
	run(ExitOK, "job_renew", "--id", id(job), "--lease_token", token)
	run(ExitOK, "job_release", "--id", id(job), "--lease_token", token, "--note", "re-queue")
	claim = run(ExitOK, "job_claim", "--id", id(job))
	run(ExitUsage, "job_finish", "--id", id(job), "--lease_token", claim["lease_token"].(string), "--trail_slug", "no-such-trail")
	run(ExitOK, "job_finish", "--id", id(job), "--lease_token", claim["lease_token"].(string), "--trail_slug", "cli-probe", "--output-pages", "wiki/agent-memory.md")
	if ev := run(ExitOK, "job_events", "--id", id(job)); len(ev["events"].([]any)) < 6 {
		t.Fatalf("events: %v", ev)
	}
	spare := run(ExitOK, "job_create", "--question", "Throwaway")
	run(ExitOK, "job_abandon", "--id", id(spare), "--note", "not needed")
	run(ExitNotFound, "job_get", "--id", "999999")
	run(ExitNotFound, "wiki_read", "--path", "wiki/no-such-page.md")
	if tax := run(ExitOK, "wiki_taxonomy"); len(tax["tags"].([]any)) == 0 {
		t.Fatalf("taxonomy: %v", tax)
	}
	if scan := run(ExitOK, "wiki_curation_scan", "--hub_min_pages", "2"); scan["counts"] == nil {
		t.Fatalf("curation scan: %v", scan)
	}

	cat, _ := Embedded()
	var missed []string
	for _, n := range cat.Names() {
		if !called[n] {
			missed = append(missed, n)
		}
	}
	if len(missed) > 0 {
		t.Fatalf("operations not exercised through the CLI: %v", missed)
	}

	// The live MCP surface (full profile) and the CLI catalog list the same
	// operations at the same revision.
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"toolProfile":"full"}}`
	resp, err := http.Post(endpoint, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var tl struct {
		Result struct {
			Tools   []struct{ Name string }   `json:"tools"`
			Catalog struct{ Revision string } `json:"catalog"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tl); err != nil {
		t.Fatal(err)
	}
	var live []string
	for _, tool := range tl.Result.Tools {
		live = append(live, tool.Name)
	}
	sort.Strings(live)
	if strings.Join(live, ",") != strings.Join(cat.Names(), ",") || tl.Result.Catalog.Revision != cat.Revision {
		t.Fatalf("live tools/list %v (%s) != CLI catalog %v (%s)", live, tl.Result.Catalog.Revision, cat.Names(), cat.Revision)
	}
}

func mustJSON(v any) string { raw, _ := json.Marshal(v); return string(raw) }
