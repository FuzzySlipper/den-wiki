package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixture = "../../internal/lint/testdata/valid"

func runCLI(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// brokenContent is a copy of the valid fixture with one warning-level finding.
func brokenContent(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	err := filepath.Walk(fixture, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(fixture, p)
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(dir, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("# notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLintJSON(t *testing.T) {
	code, out, errOut := runCLI("lint", brokenContent(t))
	if code != exitOK {
		t.Fatalf("exit %d without --fail-on; stderr: %s", code, errOut)
	}
	var rep lintReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Code != "zone.outside" || rep.Counts["warning"] != 1 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if !strings.Contains(out, `"severity": "warning"`) {
		t.Fatalf("severity not rendered by name:\n%s", out)
	}
}

func TestLintCleanJSONHasEmptyFindings(t *testing.T) {
	code, out, _ := runCLI("lint", fixture)
	if code != exitOK || !strings.Contains(out, `"findings": []`) {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
}

func TestFailOn(t *testing.T) {
	dir := brokenContent(t)
	cases := []struct {
		failOn string
		want   int
	}{
		{"error", exitOK},
		{"warning", exitFindings},
		{"info", exitFindings},
	}
	for _, c := range cases {
		// Flags after the directory are accepted too.
		if code, _, _ := runCLI("lint", dir, "--fail-on", c.failOn); code != c.want {
			t.Errorf("--fail-on %s: exit %d, want %d", c.failOn, code, c.want)
		}
	}
}

func TestTextFormat(t *testing.T) {
	code, out, _ := runCLI("lint", "--format", "text", brokenContent(t))
	if code != exitOK || !strings.Contains(out, "zone.outside") || !strings.Contains(out, "0 error, 1 warning, 0 info") {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
}

func TestUsageAndRuntimeErrors(t *testing.T) {
	cases := []struct {
		args []string
		want int
	}{
		{nil, exitUsage},
		{[]string{"frobnicate"}, exitUsage},
		{[]string{"lint"}, exitUsage},
		{[]string{"lint", "a", "b"}, exitUsage},
		{[]string{"lint", "--format", "xml", fixture}, exitUsage},
		{[]string{"lint", "--fail-on", "fatal", fixture}, exitUsage},
		{[]string{"lint", "--bogus", fixture}, exitUsage},
		{[]string{"lint", filepath.Join(t.TempDir(), "missing")}, exitRuntime},
		{[]string{"lint", "--commits", "HEAD~1..HEAD", t.TempDir()}, exitRuntime},
		{[]string{"lint", "--help"}, exitOK},
		{[]string{"help"}, exitOK},
	}
	for _, c := range cases {
		if code, _, _ := runCLI(c.args...); code != c.want {
			t.Errorf("%v: exit %d, want %d", c.args, code, c.want)
		}
	}
}

func TestListRules(t *testing.T) {
	code, out, _ := runCLI("lint", "--list-rules")
	if code != exitOK || !strings.Contains(out, `"code": "naming.date"`) {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
}
