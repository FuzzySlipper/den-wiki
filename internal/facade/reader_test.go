package facade

import (
	"context"
	"reflect"
	"testing"

	"github.com/FuzzySlipper/den-wiki/internal/reader"
)

func TestReaderBackend(t *testing.T) {
	ok := &countingWiki{answers: map[string]string{
		"exec": `{"enrichedPaths":[{"path":"wiki/agent-memory.md",
			"backlinks":[{"source":"wiki/rag","title":"RAG"},{"source":"wiki/index","title":"Index"},{"source":"[Word](x)","title":"bogus"}],
			"projectHistory":[{"authorName":"claude-code","date":"2026-11-02T11:55:36Z","hash":"4c56b69","subject":"Tidy (trail:curation-2026-11-02)"},
				{"authorName":"ui","date":"2026-10-30T11:02:29Z","hash":"2bfea90","subject":"Edit in the OpenKnowledge UI: wiki/agent-memory.md (ui)"}]}]}`,
		"search": `{"results":[{"kind":"page","path":"wiki/agent-memory.md","title":"Agent memory","snippet":"memory"},
			{"kind":"page","docName":"trails/run","title":"trails/run"},{"kind":"folder","path":"wiki/ai"},{"kind":"page","path":"wiki/index.md"}]}`,
	}}
	b := NewReaderBackend(ok)
	br, err := b.Briefing(context.Background(), "wiki/agent-memory.md")
	if err != nil {
		t.Fatal(err)
	}
	want := reader.Briefing{
		Backlinks: []reader.Backlink{{Path: "wiki/rag.md", Title: "RAG"}},
		History: []reader.Change{
			{Date: "2026-11-02", Author: "claude-code", Subject: "Tidy (trail:curation-2026-11-02)", Commit: "4c56b69", Trail: "curation-2026-11-02"},
			{Date: "2026-10-30", Author: "ui", Subject: "Edit in the OpenKnowledge UI: wiki/agent-memory.md (ui)", Commit: "2bfea90"},
		},
	}
	if !reflect.DeepEqual(br, want) {
		t.Errorf("briefing %+v", br)
	}
	if c := ok.calls[0]; c.identity != "reader" || c.tool != "exec" {
		t.Errorf("call %+v", c)
	}
	hits, err := b.Search(context.Background(), "memory", 20)
	if err != nil {
		t.Fatal(err)
	}
	wantHits := []reader.Hit{{Path: "wiki/agent-memory.md", Title: "Agent memory", Snippet: "memory"}, {Path: "trails/run.md", Title: "trails/run"}}
	if !reflect.DeepEqual(hits, wantHits) {
		t.Errorf("hits %+v", hits)
	}
	if _, err := b.Briefing(context.Background(), "wiki/a page.md"); err == nil {
		t.Error("a path OK's exec cannot take was sent")
	}
}
