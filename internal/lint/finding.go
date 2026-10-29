package lint

import (
	"fmt"
	"sort"
	"strings"
)

// Severity orders findings. Rules are findings, never write blocks; the CLI
// turns severities into a non-zero exit only when asked to with --fail-on.
type Severity int

const (
	Info Severity = iota + 1
	Warning
	Error
)

func (s Severity) String() string {
	switch s {
	case Info:
		return "info"
	case Warning:
		return "warning"
	case Error:
		return "error"
	}
	return fmt.Sprintf("severity(%d)", int(s))
}

// MarshalText makes severities render as their names in JSON output.
func (s Severity) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// UnmarshalText reads the names MarshalText writes.
func (s *Severity) UnmarshalText(b []byte) error {
	v, err := ParseSeverity(string(b))
	if err != nil {
		return err
	}
	*s = v
	return nil
}

// ParseSeverity accepts the names that String returns.
func ParseSeverity(name string) (Severity, error) {
	switch strings.ToLower(name) {
	case "info":
		return Info, nil
	case "warning":
		return Warning, nil
	case "error":
		return Error, nil
	}
	return 0, fmt.Errorf("unknown severity %q (want info, warning or error)", name)
}

// Finding is one rule violation. Code is stable and safe to switch on; Message
// is for people and may change.
type Finding struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Path     string   `json:"path,omitempty"`
	Line     int      `json:"line,omitempty"`
	Commit   string   `json:"commit,omitempty"`
	Message  string   `json:"message"`
}

func (f Finding) String() string {
	loc := f.Path
	if f.Line > 0 {
		loc = fmt.Sprintf("%s:%d", loc, f.Line)
	}
	if f.Commit != "" {
		if loc != "" {
			loc += " "
		}
		loc += "commit " + shortSHA(f.Commit)
	}
	return fmt.Sprintf("%-7s %-28s %s  %s", f.Severity, f.Code, loc, f.Message)
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Commit != b.Commit {
			return a.Commit < b.Commit
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Message < b.Message
	})
}

// Counts tallies findings by severity name.
func Counts(fs []Finding) map[string]int {
	c := map[string]int{"error": 0, "warning": 0, "info": 0}
	for _, f := range fs {
		c[f.Severity.String()]++
	}
	return c
}

// AtLeast reports whether any finding is at or above min.
func AtLeast(fs []Finding, min Severity) bool {
	for _, f := range fs {
		if f.Severity >= min {
			return true
		}
	}
	return false
}
