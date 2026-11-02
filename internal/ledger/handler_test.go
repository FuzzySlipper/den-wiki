package ledger

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	s, _, _ := newTestService(t)
	mux := http.NewServeMux()
	NewHandler(s).RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func call(t *testing.T, srv *httptest.Server, method, path, body string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s %s: response is not a JSON object: %s", method, path, raw)
	}
	return resp.StatusCode, out
}

func errCode(out map[string]any) string {
	e, _ := out["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func TestHandlerFlow(t *testing.T) {
	srv := newTestServer(t)
	status, job := call(t, srv, "POST", "/v1/jobs", `{"question":"How do agents keep memory?","requested_by":"owner","priority":2}`)
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, job)
	}
	id := int64(job["id"].(float64))
	status, claim := call(t, srv, "POST", fmt.Sprintf("/v1/jobs/%d/claim", id), `{"actor":"alice","harness":"codex"}`)
	if status != http.StatusOK || claim["lease_token"] == "" {
		t.Fatalf("claim: %d %v", status, claim)
	}
	token := claim["lease_token"].(string)
	status, out := call(t, srv, "POST", fmt.Sprintf("/v1/jobs/%d/claim", id), `{"actor":"bob"}`)
	if status != http.StatusConflict || errCode(out) != CodeClaimConflict {
		t.Fatalf("second claim: %d %v", status, out)
	}
	status, out = call(t, srv, "POST", fmt.Sprintf("/v1/jobs/%d/finish", id), fmt.Sprintf(`{"actor":"alice","lease_token":%q,"trail_slug":"memory-run"}`, token))
	if status != http.StatusOK || out["status"] != StatusDone {
		t.Fatalf("finish: %d %v", status, out)
	}
	status, out = call(t, srv, "GET", fmt.Sprintf("/v1/jobs/%d/events", id), "")
	if status != http.StatusOK || len(out["events"].([]any)) != 3 {
		t.Fatalf("events: %d %v", status, out)
	}
	status, out = call(t, srv, "GET", "/v1/jobs?status=done&limit=1", "")
	if status != http.StatusOK || len(out["jobs"].([]any)) != 1 || out["next_offset"] != nil {
		t.Fatalf("list: %d %v", status, out)
	}
	if _, present := out["next_offset"]; !present {
		t.Fatal("next_offset missing from last page; it must be present and null")
	}
}

func TestHandlerListsByKind(t *testing.T) {
	srv := newTestServer(t)
	for _, body := range []string{`{"question":"a","requested_by":"owner"}`, `{"question":"b","requested_by":"owner","kind":"calibration"}`} {
		if status, out := call(t, srv, "POST", "/v1/jobs", body); status != http.StatusCreated {
			t.Fatalf("create: %d %v", status, out)
		}
	}
	status, out := call(t, srv, "GET", "/v1/jobs?kind=calibration", "")
	jobs, _ := out["jobs"].([]any)
	if status != http.StatusOK || len(jobs) != 1 || jobs[0].(map[string]any)["kind"] != "calibration" {
		t.Fatalf("list by kind: %d %v", status, out)
	}
	if status, out := call(t, srv, "GET", "/v1/jobs?kind=survey", ""); status != http.StatusBadRequest || errCode(out) != CodeValidationFailed {
		t.Fatalf("unknown kind: %d %v", status, out)
	}
}

func TestHandlerErrors(t *testing.T) {
	srv := newTestServer(t)
	_, job := call(t, srv, "POST", "/v1/jobs", `{"question":"q","requested_by":"owner"}`)
	id := int64(job["id"].(float64))
	cases := []struct {
		method, path, body string
		status             int
		code               string
	}{
		{"POST", "/v1/jobs", `{"question":"q","requested_by":"owner","priorty":2}`, 400, CodeValidationFailed},
		{"POST", "/v1/jobs", `{"question":"q","requested_by":"owner"} {}`, 400, CodeValidationFailed},
		{"POST", "/v1/jobs", `not json`, 400, CodeValidationFailed},
		{"POST", "/v1/jobs", `{"requested_by":"owner"}`, 400, CodeValidationFailed},
		{"GET", "/v1/jobs/9999", "", 404, CodeNotFound},
		{"GET", "/v1/jobs/abc", "", 400, CodeValidationFailed},
		{"GET", "/v1/jobs?colour=red", "", 400, CodeValidationFailed},
		{"GET", "/v1/jobs?limit=x", "", 400, CodeValidationFailed},
		{"GET", "/v1/jobs?limit=1000", "", 400, CodeValidationFailed},
		{"POST", fmt.Sprintf("/v1/jobs/%d/finish", id), `{"actor":"a","lease_token":"t"}`, 400, CodeValidationFailed},
		{"POST", fmt.Sprintf("/v1/jobs/%d/renew", id), `{"actor":"a","lease_token":"t"}`, 409, CodeInvalidTransition},
		{"PATCH", fmt.Sprintf("/v1/jobs/%d", id), `{"actor":"a","title":"x"}`, 400, CodeValidationFailed},
	}
	for _, c := range cases {
		status, out := call(t, srv, c.method, c.path, c.body)
		if status != c.status || errCode(out) != c.code {
			t.Errorf("%s %s %s: got %d %v, want %d %s", c.method, c.path, c.body, status, out, c.status, c.code)
		}
	}
}
