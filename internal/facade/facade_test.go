package facade

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FuzzySlipper/den-wiki/internal/ledger"
	"github.com/FuzzySlipper/den-wiki/internal/okclient"
)

// countingWiki is a WikiBackend that records calls and answers from a table.
type countingWiki struct {
	mu      sync.Mutex
	calls   []wikiCall
	answers map[string]string
}

type wikiCall struct {
	identity, tool string
	args           any
}

func (w *countingWiki) Ping(context.Context) error { return nil }

func (w *countingWiki) Call(_ context.Context, identity, tool string, args any) (okclient.CallResult, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, wikiCall{identity, tool, args})
	ans, ok := w.answers[tool]
	if !ok {
		ans = `{}`
	}
	return okclient.CallResult{Text: ans, Structured: json.RawMessage(ans)}, nil
}

func (w *countingWiki) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.calls)
}

type harness struct {
	srv   *httptest.Server
	wiki  *countingWiki
	reads *SQLiteReadLog
	logs  *bytes.Buffer
}

func newHarness(t *testing.T, wiki *countingWiki) *harness {
	t.Helper()
	if wiki == nil {
		return newHarnessWith(t, nil, "")
	}
	h := newHarnessWith(t, wiki, "")
	h.wiki = wiki
	return h
}

// newHarnessWith serves the facade over backend (nil for none) and
// contentDir.
func newHarnessWith(t *testing.T, backend WikiBackend, contentDir string) *harness {
	t.Helper()
	dir := t.TempDir()
	db := filepath.Join(dir, "den-wiki.db")
	store, err := ledger.OpenSQLite(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	reads, err := OpenReadLog(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reads.Close() })
	logs := &bytes.Buffer{}
	d := Deps{
		Ledger: ledger.NewService(store, time.Now), Reads: reads,
		Logger: slog.New(slog.NewJSONHandler(logs, nil)), Clock: time.Now,
	}
	if backend != nil {
		d.Wiki, d.ContentDir = backend, contentDir
	}
	reg, err := NewRegistry(d)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewServer(d, reg))
	t.Cleanup(srv.Close)
	return &harness{srv: srv, reads: reads, logs: logs}
}

func (h *harness) rpc(t *testing.T, agent, profile, method string, params any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/mcp", bytes.NewReader(body))
	if agent != "" {
		req.Header.Set(AgentHeader, agent)
	}
	if profile != "" {
		req.Header.Set(ProfileHeader, profile)
	}
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s: %s", method, raw)
	}
	return out
}

// call runs tools/call and returns (structuredContent, isError).
func (h *harness) call(t *testing.T, agent, tool string, args any) (map[string]any, bool) {
	t.Helper()
	out := h.rpc(t, agent, "", "tools/call", map[string]any{"name": tool, "arguments": args})
	if e, ok := out["error"]; ok {
		return map[string]any{"rpc_error": e}, true
	}
	res := out["result"].(map[string]any)
	sc, _ := res["structuredContent"].(map[string]any)
	return sc, res["isError"].(bool)
}

func errorCode(sc map[string]any) string {
	e, _ := sc["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func toolNames(out map[string]any) []string {
	res := out["result"].(map[string]any)
	var names []string
	for _, tl := range res["tools"].([]any) {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	return names
}

func TestDiscoveryProfiles(t *testing.T) {
	h := newHarness(t, &countingWiki{})
	def := toolNames(h.rpc(t, "", "", "tools/list", nil))
	full := toolNames(h.rpc(t, "", "full", "tools/list", nil))
	if len(def) == 0 || len(full) <= len(def) {
		t.Fatalf("default %d tools, full %d", len(def), len(full))
	}
	joined := " " + strings.Join(def, " ") + " "
	for _, want := range []string{"job_create", "job_claim", "job_finish"} {
		if !strings.Contains(joined, " "+want+" ") {
			t.Errorf("default discovery lacks %s: %v", want, def)
		}
	}
	for _, hidden := range []string{"job_abandon", "wiki_lint"} {
		if strings.Contains(joined, " "+hidden+" ") {
			t.Errorf("default discovery lists long-tail %s", hidden)
		}
	}
	cat := h.rpc(t, "", "", "tools/list", nil)["result"].(map[string]any)["catalog"].(map[string]any)
	if !strings.HasPrefix(cat["revision"].(string), "catalog-") || int(cat["listed"].(float64)) != len(def) {
		t.Fatalf("catalog %v", cat)
	}
	// Long-tail operations are callable by exact name though not listed.
	if sc, isErr := h.call(t, "", "wiki_lint_rules", map[string]any{}); isErr || sc["rules"] == nil {
		t.Fatalf("long-tail call: %v", sc)
	}
	out := h.rpc(t, "", "admin", "tools/list", nil)
	if out["error"] == nil {
		t.Fatal("unknown profile accepted")
	}
}

func TestLedgerThroughFacade(t *testing.T) {
	h := newHarness(t, &countingWiki{})
	job, isErr := h.call(t, "owner", "job_create", map[string]any{"question": "How do agents keep memory?"})
	if isErr || job["requested_by"] != "owner" {
		t.Fatalf("create: %v", job)
	}
	id := job["id"]
	claim, isErr := h.call(t, "alice", "job_claim", map[string]any{"id": id, "harness": "codex"})
	if isErr {
		t.Fatalf("claim: %v", claim)
	}
	second, isErr := h.call(t, "bob", "job_claim", map[string]any{"id": id})
	if !isErr || errorCode(second) != "claim_conflict" {
		t.Fatalf("second claim: %v", second)
	}
	done, isErr := h.call(t, "alice", "job_finish", map[string]any{"id": id, "lease_token": claim["lease_token"], "trail_slug": "memory-run"})
	if isErr || done["status"] != "done" {
		t.Fatalf("finish: %v", done)
	}
	evs, _ := h.call(t, "", "job_events", map[string]any{"id": id})
	if n := len(evs["events"].([]any)); n != 3 {
		t.Fatalf("%d events", n)
	}
}

func TestIdentityAndStrictness(t *testing.T) {
	h := newHarness(t, &countingWiki{})
	sc, isErr := h.call(t, "", "job_create", map[string]any{"question": "q"})
	if !isErr || errorCode(sc) != "missing_agent" {
		t.Fatalf("write without agent: %v", sc)
	}
	sc, isErr = h.call(t, "owner", "job_create", map[string]any{"question": "q", "requested_by": "someone-else"})
	if !isErr || errorCode(sc) != "unknown_field" {
		t.Fatalf("actor spoofing field accepted: %v", sc)
	}
	sc, _ = h.call(t, "owner", "job_crate", map[string]any{})
	if sc["rpc_error"] == nil {
		t.Fatalf("unknown tool: %v", sc)
	}
	out := h.rpc(t, "", "", "tools/call", map[string]any{"name": "job_list", "arguments": map[string]any{}, "extra": 1})
	if out["error"] == nil {
		t.Fatal("unknown tools/call param accepted")
	}
}

func TestTelemetryOmitsArguments(t *testing.T) {
	h := newHarness(t, &countingWiki{})
	h.call(t, "owner", "job_create", map[string]any{"question": "secret research question"})
	h.call(t, "owner", "job_create", map[string]any{"question": "q", "bogus": true})
	logs := h.logs.String()
	if strings.Contains(logs, "secret research question") {
		t.Fatalf("telemetry logged arguments: %s", logs)
	}
	lines := strings.Split(strings.TrimSpace(logs), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 telemetry lines, got %d: %s", len(lines), logs)
	}
	var first, second map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &first)
	_ = json.Unmarshal([]byte(lines[1]), &second)
	for _, k := range []string{"requested_tool", "canonical_tool", "agent", "outcome", "retryable", "duration_ms"} {
		if _, ok := first[k]; !ok {
			t.Errorf("telemetry lacks %s: %v", k, first)
		}
	}
	if first["msg"] != "mcp_tool_call" || first["outcome"] != "success" || second["outcome"] != "unknown_field" {
		t.Fatalf("telemetry: %v / %v", first, second)
	}
}

func TestHealthAndVersion(t *testing.T) {
	h := newHarness(t, &countingWiki{})
	resp, err := http.Get(h.srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	var health map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&health)
	resp.Body.Close()
	if resp.StatusCode != 200 || health["status"] != "ok" {
		t.Fatalf("healthz %d %v", resp.StatusCode, health)
	}
	resp, _ = http.Get(h.srv.URL + "/version")
	var v map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&v)
	resp.Body.Close()
	if v["service"] != "den-wiki" || !strings.HasPrefix(v["catalog_revision"].(string), "catalog-") {
		t.Fatalf("version %v", v)
	}
	if r, _ := http.Get(h.srv.URL + "/mcp"); r.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /mcp: %d", r.StatusCode)
	}
}

func TestToolsCallAcceptsRequestMeta(t *testing.T) {
	h := newHarness(t, &countingWiki{})
	out := h.rpc(t, "alice", "", "tools/call", map[string]any{
		"name": "job_list", "arguments": map[string]any{}, "_meta": map[string]any{"progressToken": "p1"},
	})
	res, ok := out["result"].(map[string]any)
	if !ok || res["isError"].(bool) {
		t.Fatalf("tools/call with _meta: %v", out)
	}
	out = h.rpc(t, "alice", "", "tools/call", map[string]any{"name": "job_list", "arguments": map[string]any{}, "extra": true})
	if _, isRPCErr := out["error"]; !isRPCErr {
		t.Fatalf("an unknown params field was accepted: %v", out)
	}
}
