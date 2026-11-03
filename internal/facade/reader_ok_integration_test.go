//go:build okintegration

package facade

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FuzzySlipper/den-wiki/internal/ledger"
	"github.com/FuzzySlipper/den-wiki/internal/okclient"
	"github.com/FuzzySlipper/den-wiki/internal/reader"
	"github.com/FuzzySlipper/den-wiki/internal/testok"
)

// The reader against a real OpenKnowledge: backlinks and history from OK's
// briefing, and search.
func TestReaderWithOpenKnowledge(t *testing.T) {
	stack := testok.Start(t, "../..")
	rd, err := reader.New(reader.Config{
		ContentDir: stack.ContentDir,
		EditURL:    "https://edit.example",
		Backend:    NewReaderBackend(NewOKWiki(okclient.New(stack.MCP, 30*time.Second))),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rd.Close() })
	store, err := ledger.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "den-wiki.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	d := Deps{Ledger: ledger.NewService(store, time.Now), Reader: rd}
	reg, err := NewRegistry(d)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewServer(d, reg))
	t.Cleanup(srv.Close)
	get := func(path string) string {
		t.Helper()
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d\n%s", path, resp.StatusCode, b)
		}
		return string(b)
	}
	page := get("/wiki/agent-memory")
	for _, want := range []string{
		"<h1>Agent memory</h1>",
		"What links here",
		`href="/trails/agent-memory-research-2026-10-28"`,
		"Seed from den-wiki template and lint fixtures",
		`href="https://edit.example/#/wiki/agent-memory"`,
		`<h2 id="references">References</h2>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(page, "unavailable") {
		t.Errorf("OK panels unavailable:\n%s", page)
	}
	if s := get("/search?q=memory"); !strings.Contains(s, `href="/wiki/agent-memory"`) {
		t.Errorf("search did not find the page:\n%s", s)
	}
	if h := get("/healthz"); !strings.Contains(h, "ledger") {
		t.Errorf("facade routes lost behind the reader: %s", h)
	}
}
