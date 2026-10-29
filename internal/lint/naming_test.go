package lint

import "testing"

func TestCheckWikiName(t *testing.T) {
	cases := []struct {
		name string
		want []string
	}{
		{"agent-memory", nil},
		{"new-york", nil},
		{"old-norse", nil},
		{"final-fantasy-tactics", nil},
		{"http-2", nil},
		{"iso-8601", nil},
		{"rtx-2080-ti", nil},
		{"web3", nil},
		{"deadbeef-cafe", nil},
		{"AgentMemory", []string{codeNamingSlug}},
		{"agent--memory", []string{codeNamingSlug}},
		{"agent_memory", []string{codeNamingSlug}},
		{"memory-2026-10-29", []string{codeNamingDate}},
		{"memory-20261029", []string{codeNamingDate}},
		{"memory-2026-10", []string{codeNamingDate}},
		{"memory-q3-2026", []string{codeNamingDate}},
		{"memory-3f9a2c1", []string{codeNamingSHA}},
		{"memory-v2", []string{codeNamingVersion}},
		{"v3-memory", []string{codeNamingVersion}},
		{"memory-final", []string{codeNamingVersion}},
		{"memory-new", []string{codeNamingVersion}},
		{"memory-updated", []string{codeNamingVersion}},
		{"memory-old", []string{codeNamingVersion}},
		{"memory-2026-10-29-final", []string{codeNamingDate, codeNamingVersion}},
	}
	for _, c := range cases {
		got := checkWikiName("wiki/"+c.name+".md", c.name)
		codes := make([]string, len(got))
		for i, f := range got {
			codes[i] = f.Code
		}
		if len(codes) != len(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, codes, c.want)
			continue
		}
		for i := range codes {
			if codes[i] != c.want[i] {
				t.Errorf("%s: got %v, want %v", c.name, codes, c.want)
			}
		}
	}
}
