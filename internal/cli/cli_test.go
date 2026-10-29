package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FuzzySlipper/den-wiki/internal/registry"
)

type fakeTransport struct {
	calls []fakeCall
	reply json.RawMessage
	err   error
}

type fakeCall struct {
	agent, op string
	args      map[string]any
}

func (f *fakeTransport) Call(_ context.Context, agent, op string, args map[string]any) (json.RawMessage, error) {
	f.calls = append(f.calls, fakeCall{agent, op, args})
	if f.err != nil {
		return nil, f.err
	}
	if f.reply == nil {
		return json.RawMessage(`{"ok":true}`), nil
	}
	return f.reply, nil
}

func runCLI(t *testing.T, tr Transport, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	// An explicit empty config keeps the developer's own config out of tests.
	cfg := filepath.Join(t.TempDir(), "cli.yml")
	if err := os.WriteFile(cfg, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	args = append(args, "--config", cfg)
	code := Run(args, Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb, Transport: tr})
	return code, out.String(), errb.String()
}

func TestTopHelpListsEveryOperation(t *testing.T) {
	cat, _ := Embedded()
	code, out, _ := runCLI(t, nil, "help")
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	for _, n := range cat.Names() {
		if !strings.Contains(out, n) {
			t.Errorf("help does not list %s", n)
		}
	}
	for _, want := range []string{"--args-json", "--agent", "Exit codes", "search <terms>", "describe <operation>"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q", want)
		}
	}
}

func TestOperationHelp(t *testing.T) {
	code, out, _ := runCLI(t, nil, "job_claim", "--help")
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"den-wiki job_claim:", "Risk: write", "Needs --agent", "--id <integer> (required)", "--lease_seconds <integer>", "min 60", "Input schema:"} {
		if !strings.Contains(out, want) {
			t.Errorf("job_claim --help lacks %q:\n%s", want, out)
		}
	}
	_, out, _ = runCLI(t, nil, "help", "job_create")
	if !strings.Contains(out, "Examples:") || !strings.Contains(out, "den-wiki job_create --priority 2 --question") {
		t.Errorf("job_create help lacks a runnable example:\n%s", out)
	}
}

func TestSearchAndDescribe(t *testing.T) {
	_, out, _ := runCLI(t, nil, "search", "lease")
	var res struct {
		Operations []struct{ Name string } `json:"operations"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("search output not JSON: %v\n%s", err, out)
	}
	found := map[string]bool{}
	for _, o := range res.Operations {
		found[o.Name] = true
	}
	if !found["job_claim"] || !found["job_renew"] || found["wiki_lint_rules"] {
		t.Errorf("search lease: %v", found)
	}
	code, out, _ := runCLI(t, nil, "describe", "job_finish")
	var e Entry
	if code != ExitOK || json.Unmarshal([]byte(out), &e) != nil || e.Name != "job_finish" || e.InputSchema["properties"] == nil {
		t.Fatalf("describe: %d %s", code, out)
	}
	code, _, errOut := runCLI(t, nil, "describe", "job_finsh")
	if code != ExitUsage || !strings.Contains(errOut, `did you mean \"job_finish\"`) {
		t.Fatalf("describe typo: %d %s", code, errOut)
	}
}

func TestFlagsMirrorSchema(t *testing.T) {
	tr := &fakeTransport{}
	code, _, errOut := runCLI(t, tr, "job_list", "--statuses", "open,claimed", "--statuses", "done", "--max-priority", "2", "--limit=5")
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	got, _ := json.Marshal(tr.calls[0].args)
	if string(got) != `{"limit":5,"max_priority":2,"statuses":["open","claimed","done"]}` {
		t.Fatalf("args %s", got)
	}
	tr = &fakeTransport{}
	runCLI(t, tr, "job_edit", "--agent", "a", "--id", "3", "--clear_parent", "--note", "tidy")
	got, _ = json.Marshal(tr.calls[0].args)
	if string(got) != `{"clear_parent":true,"id":3,"note":"tidy"}` || tr.calls[0].agent != "a" {
		t.Fatalf("args %s agent %q", got, tr.calls[0].agent)
	}
	tr = &fakeTransport{}
	file := filepath.Join(t.TempDir(), "args.json")
	_ = os.WriteFile(file, []byte(`{"question":"q","priority":1}`), 0o644)
	if code, _, e := runCLI(t, tr, "job_create", "--agent", "a", "--args-json", "@"+file); code != ExitOK {
		t.Fatalf("args-json file: %d %s", code, e)
	}
	got, _ = json.Marshal(tr.calls[0].args)
	if string(got) != `{"priority":1,"question":"q"}` {
		t.Fatalf("args-json args %s", got)
	}
}

// An explicit empty array reaches the transport as [], from field flags and
// from --args-json alike.
func TestEmptyArraysStayEmpty(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"job_list", "--statuses", "[]"}, `{"statuses":[]}`},
		{[]string{"job_list", "--args-json", `{"statuses":[]}`}, `{"statuses":[]}`},
		{[]string{"job_finish", "--id", "1", "--lease_token", "t", "--trail_slug", "run", "--output-pages", "[]"}, `{"id":1,"lease_token":"t","output_pages":[],"trail_slug":"run"}`},
		{[]string{"job_finish", "--args-json", `{"id":1,"lease_token":"t","trail_slug":"run","output_pages":[]}`}, `{"id":1,"lease_token":"t","output_pages":[],"trail_slug":"run"}`},
	} {
		tr := &fakeTransport{}
		code, _, errOut := runCLI(t, tr, append(c.args, "--agent", "a")...)
		if code != ExitOK || len(tr.calls) != 1 {
			t.Fatalf("%v: exit %d, %d calls: %s", c.args, code, len(tr.calls), errOut)
		}
		if got, _ := json.Marshal(tr.calls[0].args); string(got) != c.want {
			t.Errorf("%v: sent %s, want %s", c.args, got, c.want)
		}
	}
}

// Usage errors are caught locally: no request is made.
func TestUsageErrorsMakeNoCall(t *testing.T) {
	cases := []struct {
		args []string
		code string
		msg  string
	}{
		{[]string{"job_list", "--limt", "3"}, registry.CodeUnknownField, "did you mean --limit?"},
		{[]string{"job_list", "--args-json", `{"limt":3}`}, registry.CodeUnknownField, `did you mean \"limit\"`},
		{[]string{"job_get"}, registry.CodeInvalidArguments, "id"},
		{[]string{"job_get", "--id", "x"}, registry.CodeInvalidArguments, "not an integer"},
		{[]string{"job_list", "--limit", "500"}, registry.CodeInvalidArguments, "limit"},
		{[]string{"job_list", "stray"}, registry.CodeInvalidArguments, "unexpected argument"},
		{[]string{"job_list", "--limit", "3", "--args-json", "{}"}, registry.CodeInvalidArguments, "cannot be mixed"},
		{[]string{"job_create", "--question", "q"}, registry.CodeMissingAgent, "--agent"},
		{[]string{"job_crate"}, registry.CodeUnknownOperation, `did you mean \"job_create\"`},
		{[]string{"job_list", "--limit"}, registry.CodeInvalidArguments, "needs a value"},
	}
	for _, c := range cases {
		tr := &fakeTransport{}
		code, _, errOut := runCLI(t, tr, c.args...)
		if code != ExitUsage || !strings.Contains(errOut, `"code": "`+c.code+`"`) || !strings.Contains(errOut, c.msg) {
			t.Errorf("%v: exit %d, stderr %s", c.args, code, errOut)
		}
		if len(tr.calls) != 0 {
			t.Errorf("%v: made %d calls", c.args, len(tr.calls))
		}
	}
}

func TestErrorClassesHaveDistinctExitCodes(t *testing.T) {
	cases := []struct {
		code string
		exit int
	}{
		{"validation_failed", ExitUsage},
		{registry.CodeNotFound, ExitNotFound},
		{"claim_conflict", ExitConflict},
		{"lease_expired", ExitConflict},
		{"stale_write", ExitConflict},
		{registry.CodeBackendUnavailable, ExitUnavailable},
		{registry.CodeInternal, ExitInternal},
	}
	seen := map[int]bool{}
	for _, c := range cases {
		tr := &fakeTransport{err: &registry.Error{Code: c.code, Message: "boom"}}
		code, _, errOut := runCLI(t, tr, "job_get", "--id", "1")
		if code != c.exit {
			t.Errorf("%s: exit %d, want %d", c.code, code, c.exit)
		}
		if !strings.Contains(errOut, c.code) {
			t.Errorf("%s: stderr lacks code: %s", c.code, errOut)
		}
		seen[code] = true
	}
	if len(seen) != 5 {
		t.Errorf("want 5 distinct exit codes, got %v", seen)
	}
}

func TestUnreachableEndpointIsUnavailable(t *testing.T) {
	code, _, errOut := runCLI(t, nil, "job_get", "--id", "1", "--endpoint", "http://127.0.0.1:1/mcp")
	if code != ExitUnavailable || !strings.Contains(errOut, "cannot reach den-wiki") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
}

func TestHumanOutput(t *testing.T) {
	tr := &fakeTransport{reply: json.RawMessage(`{"id":4,"status":"open","output_pages":[],"lease":{"token":"t"}}`)}
	code, out, _ := runCLI(t, tr, "job_get", "--id", "4", "--human")
	if code != ExitOK || !strings.Contains(out, "id: 4") || !strings.Contains(out, "output_pages: (none)") || !strings.Contains(out, "  token: t") {
		t.Fatalf("human output:\n%s", out)
	}
	tr = &fakeTransport{err: &registry.Error{Code: "not_found", Message: "job 4 not found"}}
	code, _, errOut := runCLI(t, tr, "job_get", "--id", "4", "--human")
	if code != ExitNotFound || errOut != "error: not_found: job 4 not found\n" {
		t.Fatalf("human error: %d %q", code, errOut)
	}
}

func TestConfigFileSuppliesAgentAndEndpoint(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "cli.yml")
	_ = os.WriteFile(cfg, []byte("endpoint: http://127.0.0.1:1/mcp\nagent: from-config\n"), 0o644)
	tr := &fakeTransport{}
	var out, errb bytes.Buffer
	code := Run([]string{"job_create", "--question", "q", "--config", cfg}, Env{Stdout: &out, Stderr: &errb, Transport: tr})
	if code != ExitOK || tr.calls[0].agent != "from-config" {
		t.Fatalf("exit %d agent %+v %s", code, tr.calls, errb.String())
	}
	_ = os.WriteFile(cfg, []byte("endpoint: x\nagnet: typo\n"), 0o644)
	code = Run([]string{"job_get", "--id", "1", "--config", cfg}, Env{Stdout: &out, Stderr: &errb, Transport: tr})
	if code != ExitUsage {
		t.Fatalf("unknown config key accepted: %d", code)
	}
}

func TestHealthReadsHealthzBesideTheEndpoint(t *testing.T) {
	report := `{"checks":{"ledger":"ok","openknowledge":"ok"},"status":"ok"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, report)
	}))
	defer srv.Close()
	if code, out, e := runCLI(t, nil, "health", "--endpoint", srv.URL+"/mcp"); code != ExitOK || !strings.Contains(out, `"openknowledge": "ok"`) {
		t.Fatalf("healthy: exit %d\n%s%s", code, out, e)
	}
	report = `{"checks":{"ledger":"ok","openknowledge":"unavailable: refused"},"status":"ok"}`
	if code, _, e := runCLI(t, nil, "health", "--endpoint", srv.URL+"/mcp"); code != ExitUnavailable || !strings.Contains(e, "unhealthy: openknowledge") {
		t.Fatalf("degraded backend: exit %d %s", code, e)
	}
	srv.Close()
	if code, _, e := runCLI(t, nil, "health", "--endpoint", srv.URL+"/mcp"); code != ExitUnavailable || !strings.Contains(e, "unreachable") {
		t.Fatalf("down: exit %d %s", code, e)
	}
}
