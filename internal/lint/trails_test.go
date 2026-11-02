package lint

import (
	"strings"
	"testing"
)

func TestListTrailsByKind(t *testing.T) {
	root := t.TempDir()
	run := func(kind, started, extra string) string {
		return "---\ntype: " + kind + "\nquestion: \"Q | " + kind + "\"\nstarted: " + started + "\nagent: a\nharness: codex\n" + extra + "---\n\nbody\n"
	}
	writeContent(t, root, map[string]string{
		"trails/memory-2026-10-01.md":                  run("trail", "2026-10-01T00:00:00Z", "job: 7\nfinished: 2026-10-01T02:00:00Z\n"),
		"trails/cal-two-2026-10-05.md":                 run("calibration", "2026-10-05T00:00:00Z", ""),
		"trails/calibration-01-politics-2026-10-03.md": run("trail", "2026-10-03T00:00:00Z", "finished: 2026-10-04\n"),
		"trails/curation-2026-10-02-nightly.md":        run("trail", "2026-10-02", ""),
		"trails/war-round-synthesis-2026-10-04.md":     run("round-synthesis", "2026-10-04", ""),
		"trails/index.md":                              "# Index\n",
	})
	all, err := ListTrails(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Trails) != 5 || all.Counts[KindResearch] != 1 || all.Counts[KindCalibration] != 2 || all.Counts[KindCuration] != 1 || all.Counts[KindRoundSynthesis] != 1 {
		t.Fatalf("all trails: %+v", all)
	}
	cal, err := ListTrails(root, KindCalibration)
	if err != nil {
		t.Fatal(err)
	}
	var slugs []string
	for _, e := range cal.Trails {
		slugs = append(slugs, e.Slug)
	}
	if strings.Join(slugs, ",") != "cal-two-2026-10-05,calibration-01-politics-2026-10-03" {
		t.Fatalf("calibration trails, newest first: %v", slugs)
	}
	if cal.Counts[KindResearch] != 1 {
		t.Fatalf("counts should cover every kind when filtering: %v", cal.Counts)
	}
	md := cal.Markdown
	for _, want := range []string{
		"| [cal-two-2026-10-05](/trails/cal-two-2026-10-05.md) | calibration | 2026-10-05 | running | a (codex) | Q \\| calibration |",
		"| 2026-10-03 | 2026-10-04 |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("listing lacks %q:\n%s", want, md)
		}
	}
	research, err := ListTrails(root, KindResearch)
	if err != nil {
		t.Fatal(err)
	}
	if len(research.Trails) != 1 || research.Trails[0].Job != "7" {
		t.Fatalf("research trails: %+v", research.Trails)
	}
	if _, err := ListTrails(root, "survey"); err == nil {
		t.Fatal("an unknown kind should be an error")
	}
}
