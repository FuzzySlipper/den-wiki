// Command den-wiki is the den-wiki CLI. For now it has one operation, lint,
// which checks a content directory against docs/content-contract.md.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/FuzzySlipper/den-wiki/internal/lint"
)

// Exit codes. Findings alone never fail a run; --fail-on opts in.
const (
	exitOK       = 0
	exitFindings = 1
	exitUsage    = 2
	exitRuntime  = 3
)

const usage = `den-wiki: tools for the den-wiki research knowledge base.

Usage:
  den-wiki lint [flags] <content-dir>   check content against the content contract
  den-wiki help                         show this help

Run "den-wiki lint --help" for lint flags.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	switch args[0] {
	case "lint":
		return runLint(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return exitOK
	}
	fmt.Fprintf(stderr, "den-wiki: unknown operation %q\n\n%s", args[0], usage)
	return exitUsage
}

type lintReport struct {
	Root     string         `json:"root"`
	Commits  string         `json:"commits,omitempty"`
	Counts   map[string]int `json:"counts"`
	Findings []lint.Finding `json:"findings"`
}

func runLint(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("den-wiki lint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "json", "output format: json or text")
	failOn := fs.String("fail-on", "", "exit 1 when any finding is at or above this severity: info, warning or error")
	commits := fs.String("commits", "", "also check the commits in this git revision range, e.g. origin/main..HEAD")
	listRules := fs.Bool("list-rules", false, "print every finding code with its severity and exit")
	fs.Usage = func() {
		fmt.Fprint(stderr, `Usage: den-wiki lint [flags] <content-dir>

Checks zone placement, naming under wiki/, frontmatter against the schemas,
tags and types against taxonomy.yml, source integrity, citations, trail
sections and, with --commits, the commit convention. Findings carry a stable
code and a severity; they are reports, not write blocks, so the exit code is 0
unless --fail-on is given.

Exit codes: 0 ok, 1 findings at or above --fail-on, 2 usage error,
3 content or git could not be read.

Flags:
`)
		fs.PrintDefaults()
	}

	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return exitOK
			}
			return exitUsage
		}
		if fs.NArg() == 0 {
			break
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}

	if *format != "json" && *format != "text" {
		fmt.Fprintf(stderr, "den-wiki lint: --format must be json or text, not %q\n", *format)
		return exitUsage
	}
	var min lint.Severity
	if *failOn != "" {
		s, err := lint.ParseSeverity(*failOn)
		if err != nil {
			fmt.Fprintf(stderr, "den-wiki lint: --fail-on: %v\n", err)
			return exitUsage
		}
		min = s
	}

	if *listRules {
		return printRules(stdout, *format)
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "den-wiki lint: give exactly one content directory")
		fs.Usage()
		return exitUsage
	}

	root := positional[0]
	findings, err := lint.Lint(root, lint.Options{Commits: *commits})
	if err != nil {
		fmt.Fprintf(stderr, "den-wiki lint: %v\n", err)
		return exitRuntime
	}

	if *format == "json" {
		if findings == nil {
			findings = []lint.Finding{}
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(lintReport{Root: root, Commits: *commits, Counts: lint.Counts(findings), Findings: findings}); err != nil {
			fmt.Fprintf(stderr, "den-wiki lint: %v\n", err)
			return exitRuntime
		}
	} else {
		for _, f := range findings {
			fmt.Fprintln(stdout, f)
		}
		c := lint.Counts(findings)
		fmt.Fprintf(stdout, "%d error, %d warning, %d info\n", c["error"], c["warning"], c["info"])
	}

	if min != 0 && lint.AtLeast(findings, min) {
		return exitFindings
	}
	return exitOK
}

func printRules(stdout io.Writer, format string) int {
	rules := lint.Rules()
	if format == "json" {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rules); err != nil {
			return exitRuntime
		}
		return exitOK
	}
	for _, r := range rules {
		fmt.Fprintf(stdout, "%-28s %-7s %s\n", r.Code, r.Severity, r.Summary)
	}
	return exitOK
}
