package registry

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type echoArgs struct {
	Query string   `json:"query" required:"true" desc:"What to find."`
	Limit int      `json:"limit,omitempty" min:"1" max:"50"`
	Tags  []string `json:"tags,omitempty"`
	Kind  string   `json:"kind,omitempty" enum:"a,b"`
}

func testRegistry(t *testing.T, calls *int) *Registry {
	t.Helper()
	echo := New(Operation{Name: "wiki_search", Description: "search", Risk: RiskRead, Tier: TierDefault, Backend: "test", Aliases: []string{"search"}},
		func(_ context.Context, c *Call, a echoArgs) (any, error) {
			*calls++
			c.NoteRead(a.Query, []string{"wiki/a.md"}, nil)
			return map[string]any{"query": a.Query, "agent": c.Agent}, nil
		})
	write := New(Operation{Name: "wiki_write", Description: "write", Risk: RiskWrite, Backend: "test"},
		func(_ context.Context, _ *Call, _ struct {
			Path string `json:"path" required:"true"`
		},
		) (any, error) {
			*calls++
			return "ok", nil
		})
	r, err := NewRegistry(echo, write)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSchemaGeneratedFromArgs(t *testing.T) {
	calls := 0
	op, _ := testRegistry(t, &calls).Resolve("wiki_search")
	s := op.InputSchema()
	raw, _ := json.Marshal(s)
	for _, want := range []string{`"additionalProperties":false`, `"required":["query"]`, `"maximum":50`, `"enum":["a","b"]`, `"items":{"type":"string"}`, `"description":"What to find."`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("schema lacks %s: %s", want, raw)
		}
	}
	if got := strings.Join(op.Fields(), ","); got != "kind,limit,query,tags" {
		t.Errorf("fields = %s", got)
	}
}

func TestInvokeRejectsBeforeHandler(t *testing.T) {
	calls := 0
	r := testRegistry(t, &calls)
	ctx := context.Background()
	cases := []struct {
		name, tool, agent, args, code, msg string
	}{
		{"unknown field with suggestion", "wiki_search", "", `{"query":"x","limt":3}`, CodeUnknownField, `"limt" (did you mean "limit"?)`},
		{"missing required", "wiki_search", "", `{"limit":3}`, CodeInvalidArguments, "requires query"},
		{"wrong type", "wiki_search", "", `{"query":3}`, CodeInvalidArguments, "query"},
		{"not an object", "wiki_search", "", `[1]`, CodeInvalidArguments, "JSON object"},
		{"write without agent", "wiki_write", "", `{"path":"wiki/a.md"}`, CodeMissingAgent, "X-Den-Wiki-Agent"},
		{"unknown tool", "wiki_serch", "", `{}`, CodeUnknownOperation, `did you mean "wiki_search"`},
	}
	for _, c := range cases {
		_, err := r.Invoke(ctx, c.tool, c.agent, json.RawMessage(c.args))
		e := ErrorOf(err)
		if e.Code != c.code || !strings.Contains(e.Message, c.msg) {
			t.Errorf("%s: got %s %q", c.name, e.Code, e.Message)
		}
	}
	if calls != 0 {
		t.Fatalf("handler ran %d times for rejected calls", calls)
	}
}

func TestInvokeRunsAndNotesReads(t *testing.T) {
	calls := 0
	r := testRegistry(t, &calls)
	res, err := r.Invoke(context.Background(), "search", "agent-a", json.RawMessage(`{"query":"memory"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Canonical != "wiki_search" || calls != 1 {
		t.Fatalf("alias not resolved: %+v", res)
	}
	if res.Reads == nil || res.Reads.Query != "memory" || len(res.Reads.Returned) != 1 {
		t.Fatalf("read note: %+v", res.Reads)
	}
	if _, err := r.Invoke(context.Background(), "wiki_write", "agent-a", json.RawMessage(`{"path":"wiki/a.md"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestProfilesAndRevision(t *testing.T) {
	calls := 0
	r := testRegistry(t, &calls)
	def, _ := r.Listed(ProfileDefault)
	full, _ := r.Listed(ProfileFull)
	if len(def) != 1 || len(full) != 2 {
		t.Fatalf("default %d, full %d", len(def), len(full))
	}
	if _, err := r.Listed("admin"); err == nil {
		t.Fatal("unknown profile accepted")
	}
	rev := r.Revision()
	if !strings.HasPrefix(rev, "catalog-") || rev != testRegistry(t, &calls).Revision() {
		t.Fatalf("revision unstable: %s", rev)
	}
	other, _ := NewRegistry(New(Operation{Name: "wiki_search", Description: "search!", Risk: RiskRead, Backend: "test"},
		func(context.Context, *Call, echoArgs) (any, error) { return nil, nil }))
	if other.Revision() == rev {
		t.Fatal("revision did not change with the surface")
	}
}

func TestRegistryRejectsBadDefinitions(t *testing.T) {
	op := func(name string, aliases ...string) *Operation {
		return New(Operation{Name: name, Description: "d", Risk: RiskRead, Backend: "b", Aliases: aliases},
			func(context.Context, *Call, struct{}) (any, error) { return nil, nil })
	}
	bad := [][]*Operation{
		{op("a"), op("a")},
		{op("Bad-Name")},
		{op("a", "b"), op("b")},
		{op("a", "x"), op("c", "x")},
	}
	for i, ops := range bad {
		if _, err := NewRegistry(ops...); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

func TestClassOf(t *testing.T) {
	cases := map[string]Class{
		CodeUnknownField: ClassUsage, "validation_failed": ClassUsage, CodeNotFound: ClassNotFound,
		"claim_conflict": ClassConflict, "lease_expired": ClassConflict, "stale_write": ClassConflict,
		CodeBackendUnavailable: ClassUnavailable, CodeInternal: ClassInternal, CodeBackendError: ClassInternal,
	}
	for code, want := range cases {
		if got := ClassOf(code); got != want {
			t.Errorf("ClassOf(%s) = %s, want %s", code, got, want)
		}
	}
}
