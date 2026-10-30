//go:build okintegration

package facade

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FuzzySlipper/den-wiki/internal/ledger"
	"github.com/FuzzySlipper/den-wiki/internal/lint"
	"github.com/FuzzySlipper/den-wiki/internal/okclient"
	"github.com/FuzzySlipper/den-wiki/internal/testok"
)

// countedWiki counts the calls that reach OpenKnowledge.
type countedWiki struct {
	WikiBackend
	n atomic.Int64
}

func (c *countedWiki) Call(ctx context.Context, identity, tool string, args any) (okclient.CallResult, error) {
	c.n.Add(1)
	return c.WikiBackend.Call(ctx, identity, tool, args)
}

const probeTrail = `---
type: trail
question: Does the facade keep provenance end to end?
started: 2026-10-29T13:00:00Z
agent: alpha
harness: claude-code
---

## Question and scope

End-to-end facade probe.

## Prior coverage checked

wiki/agent-memory.md.

## Queries and methods

None.

## Sources

None.

## Dead ends

None.

## Changes made

- [Agent durability](/wiki/agent-durability.md)

## Proposed changes

None.

## Open questions and gaps

None.
`

const probePage = `---
type: concept
title: Agent durability
aliases: []
tags: [ai]
curation: unreviewed
updated: 2026-10-29
---

# Agent durability

Durable memory keeps agents consistent across sessions. See [Agent memory](/wiki/agent-memory.md).
`

func TestOpenKnowledgeThroughFacade(t *testing.T) {
	stack := testok.Start(t, "../..")
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "den-wiki.db")
	store, err := ledger.OpenSQLite(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	reads, err := OpenReadLog(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reads.Close() })
	wiki := &countedWiki{WikiBackend: NewOKWiki(okclient.New(stack.MCP, 30*time.Second))}
	logs := &bytes.Buffer{}
	d := Deps{
		Ledger: ledger.NewService(store, time.Now, ledger.WithContentDir(stack.ContentDir)),
		Wiki:   wiki, ContentDir: stack.ContentDir, Reads: reads,
		Logger: slog.New(slog.NewJSONHandler(logs, nil)), Clock: time.Now,
	}
	reg, err := NewRegistry(d)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewServer(d, reg))
	t.Cleanup(srv.Close)
	h := &harness{srv: srv, reads: reads, logs: logs}

	must := func(agent, tool string, args map[string]any) map[string]any {
		t.Helper()
		sc, isErr := h.call(t, agent, tool, args)
		if isErr {
			t.Fatalf("%s as %s: %v", tool, agent, sc)
		}
		return sc
	}

	t.Run("read with links", func(t *testing.T) {
		r := must("reader", "wiki_read", map[string]any{"path": "wiki/agent-memory.md"})
		if !strings.Contains(r["content"].(string), "# Agent memory") {
			t.Fatalf("content: %v", r["content"])
		}
		fm, _ := r["frontmatter"].(map[string]any)
		if fm["title"] != "Agent memory" {
			t.Fatalf("frontmatter: %v", r["frontmatter"])
		}
		raw, _ := json.Marshal(r)
		for _, want := range []string{`"backlinks"`, `trails/agent-memory-research-2026-10-28`, `"forward_links"`, `sources/agent-memory-survey`, `"project_history"`, `Seed from den-wiki template`} {
			if !strings.Contains(string(raw), want) {
				t.Errorf("read lacks %s: %s", want, raw)
			}
		}
		links, _ := json.Marshal([]any{r["backlinks"], r["forward_links"]})
		if strings.Contains(string(links), "](") {
			t.Errorf("read passes OK's footnote misparse through: %s", links)
		}
		if z, _ := r["zone"].(map[string]any); z["zone"] != "wiki" || z["rules"] == nil {
			t.Errorf("zone rules: %v", r["zone"])
		}
	})

	t.Run("search", func(t *testing.T) {
		r := must("reader", "wiki_search", map[string]any{"query": "editable memory"})
		raw, _ := json.Marshal(r["results"])
		if !strings.Contains(string(raw), `"path":"wiki/agent-memory.md"`) {
			t.Fatalf("search results: %s", raw)
		}
	})

	t.Run("list", func(t *testing.T) {
		r := must("reader", "wiki_list", map[string]any{"folder": "wiki"})
		if z, _ := r["zone"].(map[string]any); z["zone"] != "wiki" {
			t.Fatalf("list zone: %v", r)
		}
		must("reader", "wiki_links", map[string]any{"kinds": []string{"backlinks", "forward"}, "path": "wiki/agent-memory.md"})
	})

	var trailCommit, pageCommit, editCommit map[string]any
	t.Run("write and edit with per-agent attribution", func(t *testing.T) {
		w := must("alpha", "wiki_write", map[string]any{"path": "trails/facade-probe.md", "content": probeTrail, "trail": "facade-probe", "summary": "Start facade probe trail"})
		if trailCommit, _ = w["commit"].(map[string]any); trailCommit == nil {
			t.Fatalf("trail write made no commit: %v", w)
		}
		w = must("alpha", "wiki_write", map[string]any{"path": "wiki/agent-durability.md", "content": probePage, "trail": "facade-probe", "summary": "Add agent durability page"})
		pageCommit, _ = w["commit"].(map[string]any)
		if pageCommit == nil || !strings.HasPrefix(pageCommit["author"].(string), "alpha <") {
			t.Fatalf("write commit: %v", w)
		}
		e := must("beta", "wiki_edit", map[string]any{"path": "wiki/agent-durability.md", "find": "across sessions", "replace": "across sessions and harnesses", "trail": "facade-probe", "summary": "Widen durability claim"})
		editCommit, _ = e["commit"].(map[string]any)
		if editCommit == nil || !strings.HasPrefix(editCommit["author"].(string), "beta <") {
			t.Fatalf("edit commit: %v", e)
		}
		got, _ := os.ReadFile(filepath.Join(stack.ContentDir, "wiki/agent-durability.md"))
		if !strings.Contains(string(got), "across sessions and harnesses") {
			t.Fatalf("edit not on disk: %s", got)
		}
		// wiki_write replaces an existing file: OK refuses unless told the
		// position, and research agents grow trails this way.
		grown := probeTrail + "\nAppended by a second whole-file write.\n"
		w = must("alpha", "wiki_write", map[string]any{"path": "trails/facade-probe.md", "content": grown, "trail": "facade-probe", "summary": "Grow facade probe trail"})
		if w["commit"] == nil {
			t.Fatalf("replacing write made no commit: %v", w)
		}
		if got, _ := os.ReadFile(filepath.Join(stack.ContentDir, "trails/facade-probe.md")); string(got) != grown {
			t.Fatalf("replacing write not on disk: %q", got)
		}
		log := stack.Git(t, "log", "--format=%an|%s", stack.Seed+"..HEAD")
		for _, want := range []string{"alpha|Start facade probe trail (trail:facade-probe)", "alpha|Add agent durability page (trail:facade-probe)", "beta|Widen durability claim (trail:facade-probe)", "alpha|Grow facade probe trail (trail:facade-probe)"} {
			if !strings.Contains(log, want) {
				t.Errorf("git log lacks %q:\n%s", want, log)
			}
		}
		// OK reconciles den-wiki's commits into its own history, so the agent
		// shows there as the git author; its read briefing lists every
		// trail-tagged commit with its author (project_history).
		r := must("reader", "wiki_read", map[string]any{"path": "wiki/agent-durability.md"})
		ph, _ := json.Marshal(r["project_history"])
		for _, want := range []string{`"authorName":"alpha"`, `"authorName":"beta"`, `(trail:facade-probe)`} {
			if !strings.Contains(string(ph), want) {
				t.Errorf("OK project_history lacks %s: %s", want, ph)
			}
		}
		// OK folds changes into its own history asynchronously (a wip entry
		// first, reconciled later), so wait for beta's edit to appear there.
		var hist []byte
		for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(500 * time.Millisecond) {
			hist, _ = json.Marshal(must("reader", "wiki_history", map[string]any{"path": "wiki/agent-durability.md"}))
			if strings.Contains(string(hist), "beta") || time.Now().After(deadline) {
				break
			}
		}
		if !strings.Contains(string(hist), "beta") {
			t.Errorf("OK history does not attribute the latest edit to beta within 30s: %s", hist)
		}
	})

	t.Run("commits follow the content contract", func(t *testing.T) {
		fs, err := lint.Lint(stack.ContentDir, lint.Options{Commits: stack.Seed + "..HEAD"})
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range fs {
			if strings.HasPrefix(f.Code, "commit.") {
				t.Errorf("commit finding: %s", f)
			}
		}
	})

	t.Run("links views are filtered", func(t *testing.T) {
		r := must("reader", "wiki_links", map[string]any{"kinds": []string{"dead", "orphans", "hubs"}})
		raw, _ := json.Marshal(r)
		for _, bad := range []string{".ok/templates", "](", `"docName":"wiki/index"`} {
			if strings.Contains(string(raw), bad) {
				t.Errorf("links include noise %q: %s", bad, raw)
			}
		}
	})

	t.Run("curation scan reads the content and OK's link views", func(t *testing.T) {
		r := must("librarian", "wiki_curation_scan", map[string]any{})
		counts, _ := r["counts"].(map[string]any)
		if len(counts) != len(lint.CurationClasses) || r["pages"].(float64) < 1 {
			t.Fatalf("scan: %v", r)
		}
		found := false
		for _, it := range r["items"].([]any) {
			m := it.(map[string]any)
			found = found || (m["class"] == "unreviewed" && m["path"] == "wiki/agent-durability.md")
		}
		if !found {
			t.Errorf("the probe page is not in the unreviewed backlog: %v", r["items"])
		}
		if s, _ := r["summary_markdown"].(string); !strings.Contains(s, "| unreviewed |") {
			t.Errorf("summary: %q", s)
		}
	})

	t.Run("taxonomy lists the seed tags and what pages use", func(t *testing.T) {
		r := must("reader", "wiki_taxonomy", map[string]any{})
		if r["present"] != true {
			t.Fatalf("taxonomy: %v", r)
		}
		names := map[string]bool{}
		for _, it := range r["tags"].([]any) {
			m := it.(map[string]any)
			if m["description"] == "" {
				t.Errorf("tag without description: %v", m)
			}
			names[m["name"].(string)] = true
		}
		if !names["software"] || !names["research-method"] {
			t.Errorf("tags: %v", r["tags"])
		}
	})

	t.Run("move and delete are committed", func(t *testing.T) {
		m := must("gamma", "wiki_move", map[string]any{"from": "wiki/agent-durability.md", "to": "wiki/ai/agent-durability.md", "trail": "facade-probe", "summary": "File durability under ai"})
		if m["commit"] == nil {
			t.Fatalf("move: %v", m)
		}
		d := must("gamma", "wiki_delete", map[string]any{"path": "wiki/ai/agent-durability.md", "trail": "facade-probe", "summary": "Remove probe page"})
		if d["commit"] == nil {
			t.Fatalf("delete: %v", d)
		}
		// OK may regenerate indexes in between, so look at gamma's commits
		// rather than the last two.
		log := stack.Git(t, "log", "--format=%an|%s", "--author=gamma", "-2")
		if !strings.Contains(log, "gamma|Remove probe page (trail:facade-probe)") || !strings.Contains(log, "gamma|File durability under ai (trail:facade-probe)") {
			t.Fatalf("git log:\n%s", log)
		}
	})

	t.Run("ledger claim and finish against the real trail", func(t *testing.T) {
		job := must("owner", "job_create", map[string]any{"question": "Does the facade keep provenance?"})
		claim := must("alpha", "job_claim", map[string]any{"id": job["id"], "harness": "claude-code"})
		sc, isErr := h.call(t, "alpha", "job_finish", map[string]any{"id": job["id"], "lease_token": claim["lease_token"], "trail_slug": "no-such-trail"})
		if !isErr || errorCode(sc) != "validation_failed" {
			t.Fatalf("finish with missing trail: %v", sc)
		}
		done := must("alpha", "job_finish", map[string]any{"id": job["id"], "lease_token": claim["lease_token"], "trail_slug": "facade-probe", "output_pages": []string{"wiki/agent-memory.md"}})
		if done["status"] != "done" {
			t.Fatalf("finish: %v", done)
		}
	})

	t.Run("unknown fields never reach OK", func(t *testing.T) {
		before := wiki.n.Load()
		sc, isErr := h.call(t, "alpha", "wiki_write", map[string]any{"path": "wiki/x.md", "content": "x", "trail": "facade-probe", "frontmatter": map[string]any{}})
		if !isErr || errorCode(sc) != "unknown_field" {
			t.Fatalf("unknown field: %v", sc)
		}
		sc, isErr = h.call(t, "", "wiki_write", map[string]any{"path": "wiki/x.md", "content": "x", "trail": "facade-probe"})
		if !isErr || errorCode(sc) != "missing_agent" {
			t.Fatalf("missing agent: %v", sc)
		}
		if after := wiki.n.Load(); after != before {
			t.Fatalf("%d calls reached OpenKnowledge", after-before)
		}
	})

	t.Run("read log", func(t *testing.T) {
		entries, err := reads.Recent(ctx, 1000)
		if err != nil {
			t.Fatal(err)
		}
		has := func(paths []string, want string) bool {
			for _, p := range paths {
				if p == want {
					return true
				}
			}
			return false
		}
		var sawRead, sawSearch, sawList, sawLinks bool
		for _, e := range entries {
			// Populated list and link responses record the pages they named,
			// never templates, generated indexes or dead targets.
			for _, p := range e.Returned {
				if strings.Contains(p, "/.") || strings.HasSuffix(p, "/index.md") {
					t.Errorf("%s logged non-page %s", e.Tool, p)
				}
			}
			if e.Tool == "wiki_list" && e.Query == "wiki" && has(e.Returned, "wiki/agent-memory.md") {
				sawList = true
			}
			if e.Tool == "wiki_links" && e.Query == "backlinks,forward wiki/agent-memory.md" &&
				has(e.Returned, "trails/agent-memory-research-2026-10-28.md") && has(e.Returned, "sources/agent-memory-survey.md") {
				sawLinks = true
			}
			if e.Tool == "wiki_read" && e.Agent == "reader" && len(e.Opened) == 1 && e.Opened[0] == "wiki/agent-memory.md" {
				sawRead = true
			}
			if e.Tool == "wiki_search" && e.Query == "editable memory" && len(e.Returned) > 0 {
				sawSearch = true
			}
		}
		if !sawRead || !sawSearch || !sawList || !sawLinks {
			t.Fatalf("read log entries: %+v", entries)
		}
	})

	if raw := logs.String(); strings.Contains(raw, "Durable memory keeps agents") {
		t.Fatal("telemetry logged write content")
	}
}
