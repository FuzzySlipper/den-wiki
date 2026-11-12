package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func topUsage(cat Catalog) string {
	var b strings.Builder
	b.WriteString(`den-wiki: the den-wiki research knowledge base from the command line.

Usage:
  den-wiki <operation> [--field value ...]   call an operation (JSON output)
  den-wiki <operation> --args-json '{...}'   ... with one JSON object (@file, @- for stdin)
  den-wiki <operation> --help                fields, risk and examples
  den-wiki search <terms>                    find operations
  den-wiki describe <operation>              full detail, including the input schema
  den-wiki catalog                           every operation as JSON
  den-wiki cat <path>                        print a page's Markdown (wiki_read content_only)
  den-wiki health                            den-wiki and backend health (exit 6 if unhealthy)
  den-wiki lint [flags] <content-dir>        check content locally (den-wiki lint --help)
  den-wiki serve --config <file>             run the den-wiki service

Global flags (anywhere on the line):
  --endpoint URL    den-wiki MCP endpoint (default from config, else ` + DefaultEndpoint + `)
  --agent NAME      your identity, sent with every call (required for writes)
  --config FILE     CLI config (default ` + DefaultConfigPath() + `; keys: endpoint, agent)
  --human           readable output instead of JSON

Exit codes for operations: 0 ok, 2 usage error, 4 not found, 5 conflict
(claim race, stale write, wrong status), 6 den-wiki or a backend unavailable,
7 internal error.

Operations:
`)
	for _, e := range cat.Operations {
		fmt.Fprintf(&b, "  %-18s %-11s %s\n", e.Name, e.Risk, firstSentence(e.Description))
	}
	return b.String()
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}
	return s
}

func opHelp(e Entry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "den-wiki %s: %s\n\n", e.Name, e.Description)
	fmt.Fprintf(&b, "Risk: %s. Discovery: %s. Backend: %s.", e.Risk, e.Tier, e.Backend)
	if e.NeedsAgent {
		b.WriteString(" Needs --agent.")
	}
	b.WriteString("\n")
	if len(e.Aliases) > 0 {
		fmt.Fprintf(&b, "Aliases: %s\n", strings.Join(e.Aliases, ", "))
	}
	props, _ := e.InputSchema["properties"].(map[string]any)
	required := map[string]bool{}
	if req, ok := e.InputSchema["required"].([]any); ok {
		for _, r := range req {
			required[fmt.Sprint(r)] = true
		}
	}
	names := make([]string, 0, len(props))
	for k := range props {
		names = append(names, k)
	}
	sort.Strings(names)
	b.WriteString("\nFields:\n")
	if len(names) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, n := range names {
		p, _ := props[n].(map[string]any)
		typ := typeName(p)
		req := ""
		if required[n] {
			req = " (required)"
		}
		fmt.Fprintf(&b, "  --%s %s%s\n", n, typ, req)
		if d, _ := p["description"].(string); d != "" {
			fmt.Fprintf(&b, "      %s\n", d)
		}
		var limits []string
		if en, ok := p["enum"].([]any); ok {
			s := make([]string, len(en))
			for i, v := range en {
				s[i] = fmt.Sprint(v)
			}
			limits = append(limits, "one of "+strings.Join(s, ", "))
		}
		if v, ok := p["minimum"]; ok {
			limits = append(limits, fmt.Sprintf("min %v", v))
		}
		if v, ok := p["maximum"]; ok {
			limits = append(limits, fmt.Sprintf("max %v", v))
		}
		if len(limits) > 0 {
			fmt.Fprintf(&b, "      %s\n", strings.Join(limits, "; "))
		}
	}
	b.WriteString("\nArrays take a,b,c, a JSON array, or the flag repeated. Objects take JSON.\n")
	b.WriteString("Hyphens work in flag names (--parent-id = --parent_id).\n")
	if len(e.Examples) > 0 {
		b.WriteString("\nExamples:\n")
		for _, ex := range e.Examples {
			fmt.Fprintf(&b, "  # %s\n  den-wiki %s%s\n", ex.Description, e.Name, exampleFlags(ex.Arguments))
		}
	}
	raw, _ := json.Marshal(e.InputSchema)
	fmt.Fprintf(&b, "\nInput schema:\n  %s\n", raw)
	return b.String()
}

func typeName(p map[string]any) string {
	t, _ := p["type"].(string)
	switch t {
	case "array":
		items, _ := p["items"].(map[string]any)
		return "[" + typeName(items) + ",...]"
	case "":
		return "<json>"
	}
	return "<" + t + ">"
}

func exampleFlags(args map[string]any) string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		v := args[k]
		var s string
		switch x := v.(type) {
		case string:
			s = shellQuote(x)
		case []string:
			s = shellQuote(strings.Join(x, ","))
		default:
			raw, _ := json.Marshal(x)
			s = string(raw)
			if strings.ContainsAny(s, " {}[]\"") {
				s = shellQuote(s)
			}
		}
		fmt.Fprintf(&b, " --%s %s", k, s)
	}
	return b.String()
}

func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"$`\\!*?[]{}();&|<>") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// render prints a JSON value as indented key: value lines for --human.
func render(raw json.RawMessage, indent int) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw) + "\n"
	}
	var b strings.Builder
	renderValue(&b, v, indent)
	return b.String()
}

func renderValue(b *strings.Builder, v any, indent int) {
	pad := strings.Repeat("  ", indent)
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			switch c := x[k].(type) {
			case map[string]any, []any:
				if isEmpty(c) {
					fmt.Fprintf(b, "%s%s: (none)\n", pad, k)
					continue
				}
				fmt.Fprintf(b, "%s%s:\n", pad, k)
				renderValue(b, c, indent+1)
			default:
				fmt.Fprintf(b, "%s%s: %s\n", pad, k, scalar(c))
			}
		}
	case []any:
		for _, it := range x {
			switch c := it.(type) {
			case map[string]any:
				fmt.Fprintf(b, "%s-\n", pad)
				renderValue(b, c, indent+1)
			default:
				fmt.Fprintf(b, "%s- %s\n", pad, scalar(c))
			}
		}
	default:
		fmt.Fprintf(b, "%s%s\n", pad, scalar(x))
	}
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		return len(x) == 0
	case []any:
		return len(x) == 0
	}
	return false
}

func scalar(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprint(int64(x))
		}
	}
	return fmt.Sprint(v)
}
