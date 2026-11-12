package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/FuzzySlipper/den-wiki/internal/registry"
)

// Exit codes for operation calls. 1 is left to `den-wiki lint`, where it
// means findings at or above --fail-on.
const (
	ExitOK          = 0
	ExitUsage       = 2
	ExitNotFound    = 4
	ExitConflict    = 5
	ExitUnavailable = 6
	ExitInternal    = 7
)

// DefaultEndpoint is used when neither --endpoint nor the config file names
// one. Deployment (#10466) sets the LAN endpoint in the config file.
const DefaultEndpoint = "http://127.0.0.1:7341/mcp"

// ExitFor maps an error code to an exit code.
func ExitFor(code string) int {
	switch registry.ClassOf(code) {
	case registry.ClassUsage:
		return ExitUsage
	case registry.ClassNotFound:
		return ExitNotFound
	case registry.ClassConflict:
		return ExitConflict
	case registry.ClassUnavailable:
		return ExitUnavailable
	}
	return ExitInternal
}

// Config is the optional CLI config file (YAML).
type Config struct {
	// Endpoint is the den-wiki MCP URL.
	Endpoint string `yaml:"endpoint"`
	// Agent is your identity, sent with every call.
	Agent string `yaml:"agent"`
}

// DefaultConfigPath is ~/.config/den-wiki/cli.yml.
func DefaultConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "den-wiki", "cli.yml")
}

func loadConfig(path string, explicit bool) (Config, error) {
	var c Config
	if path == "" {
		return c, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && !explicit {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Env is what Run needs from the process.
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	// Transport calls an operation on the facade. Nil uses HTTP MCP.
	Transport Transport
	// Catalog overrides the embedded catalog (tests).
	Catalog *Catalog
}

type globals struct {
	endpoint, agent, config, argsJSON string
	human, help, configSet            bool
}

// IsOperation reports whether name is a catalog operation or alias.
func IsOperation(name string) bool {
	c, err := Embedded()
	if err != nil {
		return false
	}
	_, ok := c.Find(name)
	return ok
}

// Run runs one CLI invocation: help, search, describe, catalog or an
// operation. It returns the exit code.
func Run(args []string, env Env) int {
	cat := env.Catalog
	if cat == nil {
		c, err := Embedded()
		if err != nil {
			fmt.Fprintln(env.Stderr, "den-wiki:", err)
			return ExitInternal
		}
		cat = &c
	}
	g, rest, err := splitGlobals(args)
	if err != nil {
		return usageErr(env, g.human, registry.CodeInvalidArguments, err.Error())
	}
	if len(rest) == 0 {
		fmt.Fprint(env.Stdout, topUsage(*cat))
		return ExitOK
	}
	cmd, rest := rest[0], rest[1:]
	switch cmd {
	case "help":
		if len(rest) == 0 {
			fmt.Fprint(env.Stdout, topUsage(*cat))
			return ExitOK
		}
		e, ok := cat.Find(rest[0])
		if !ok {
			return unknownOp(env, g.human, *cat, rest[0])
		}
		fmt.Fprint(env.Stdout, opHelp(e))
		return ExitOK
	case "search":
		return search(env, *cat, g, rest)
	case "describe":
		if len(rest) != 1 {
			return usageErr(env, g.human, registry.CodeInvalidArguments, "usage: den-wiki describe <operation>")
		}
		e, ok := cat.Find(rest[0])
		if !ok {
			return unknownOp(env, g.human, *cat, rest[0])
		}
		if g.human {
			fmt.Fprint(env.Stdout, opHelp(e))
		} else {
			writeJSON(env.Stdout, e)
		}
		return ExitOK
	case "catalog":
		_, _ = env.Stdout.Write(cat.Marshal())
		return ExitOK
	case "grep":
		return grepContent(env, *cat, g, rest)
	case "cat":
		if len(rest) != 1 {
			return usageErr(env, g.human, registry.CodeInvalidArguments, "usage: den-wiki cat <path>")
		}
		return catPage(env, *cat, g, rest[0])
	case "health":
		if len(rest) != 0 {
			return usageErr(env, g.human, registry.CodeInvalidArguments, "usage: den-wiki health")
		}
		return health(env, g)
	}
	e, ok := cat.Find(cmd)
	if !ok {
		return unknownOp(env, g.human, *cat, cmd)
	}
	if g.help {
		fmt.Fprint(env.Stdout, opHelp(e))
		return ExitOK
	}
	return callOp(env, e, g, rest)
}

// splitGlobals pulls the global flags out of args wherever they appear.
func splitGlobals(args []string) (globals, []string, error) {
	var g globals
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !strings.HasPrefix(a, "-") || a == "-" {
			rest = append(rest, a)
			continue
		}
		takes := func() (string, error) {
			if hasVal {
				return val, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("--%s needs a value", name)
			}
			i++
			return args[i], nil
		}
		var err error
		switch strings.ReplaceAll(name, "_", "-") {
		case "endpoint":
			g.endpoint, err = takes()
		case "agent":
			g.agent, err = takes()
		case "config":
			g.config, err = takes()
			g.configSet = true
		case "args-json":
			g.argsJSON, err = takes()
		case "human":
			g.human = !hasVal || val == "true"
		case "help", "h":
			g.help = true
		default:
			rest = append(rest, a)
		}
		if err != nil {
			return g, nil, err
		}
	}
	return g, rest, nil
}

func search(env Env, cat Catalog, g globals, terms []string) int {
	if len(terms) == 0 {
		return usageErr(env, g.human, registry.CodeInvalidArguments, "usage: den-wiki search <terms>")
	}
	type hit struct {
		Name        string        `json:"name"`
		Risk        registry.Risk `json:"risk"`
		Tier        registry.Tier `json:"tier"`
		Description string        `json:"description"`
	}
	hits := []hit{}
	for _, e := range cat.Operations {
		hay := strings.ToLower(e.Name + " " + strings.Join(e.Aliases, " ") + " " + e.Description + " " + e.Backend)
		all := true
		for _, t := range terms {
			if !strings.Contains(hay, strings.ToLower(t)) {
				all = false
				break
			}
		}
		if all {
			hits = append(hits, hit{e.Name, e.Risk, e.Tier, e.Description})
		}
	}
	if g.human {
		if len(hits) == 0 {
			fmt.Fprintln(env.Stdout, "no operations match; try fewer terms, or den-wiki catalog")
		}
		for _, h := range hits {
			fmt.Fprintf(env.Stdout, "%-18s %-11s %s\n", h.Name, h.Risk, firstSentence(h.Description))
		}
		return ExitOK
	}
	writeJSON(env.Stdout, map[string]any{"operations": hits})
	return ExitOK
}

// settings resolves the config file, endpoint and agent from flags and config.
func settings(g globals) (cfgPath, endpoint, agent string, err error) {
	cfgPath = g.config
	if !g.configSet {
		cfgPath = DefaultConfigPath()
	}
	cfg, err := loadConfig(cfgPath, g.configSet)
	if err != nil {
		return cfgPath, "", "", err
	}
	return cfgPath, firstNonEmpty(g.endpoint, cfg.Endpoint, DefaultEndpoint), firstNonEmpty(g.agent, cfg.Agent), nil
}

// health reads /healthz beside the MCP endpoint (.../mcp -> .../healthz),
// prints it, and exits 0 only when den-wiki and every backend report ok.
func health(env Env, g globals) int {
	_, endpoint, _, err := settings(g)
	if err != nil {
		return usageErr(env, g.human, registry.CodeInvalidArguments, "config: "+err.Error())
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return usageErr(env, g.human, registry.CodeInvalidArguments, "endpoint: "+err.Error())
	}
	u.Path = strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), "/mcp") + "/healthz"
	unavailable := func(msg string) int {
		return fail(env, g.human, &registry.Error{Code: registry.CodeBackendUnavailable, Message: msg, Retryable: true})
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Get(u.String())
	if err != nil {
		return unavailable("den-wiki is unreachable at " + u.String() + ": " + err.Error())
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var h struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return unavailable(fmt.Sprintf("%s answered %d without a health report", u.String(), resp.StatusCode))
	}
	if g.human {
		fmt.Fprint(env.Stdout, render(raw, 0))
	} else {
		writeRaw(env.Stdout, raw)
	}
	var bad []string
	for name, v := range h.Checks {
		if v != "ok" {
			bad = append(bad, name)
		}
	}
	if h.Status != "ok" || len(bad) > 0 {
		sort.Strings(bad)
		return unavailable("unhealthy: " + firstNonEmpty(strings.Join(bad, ", "), "status "+h.Status))
	}
	return ExitOK
}

func callOp(env Env, e Entry, g globals, rest []string) int {
	cfgPath, endpoint, agent, err := settings(g)
	if err != nil {
		return usageErr(env, g.human, registry.CodeInvalidArguments, "config: "+err.Error())
	}
	args, err := buildArgs(e, g, rest, env.Stdin)
	if err != nil {
		return fail(env, g.human, registry.ErrorOf(err))
	}
	if err := checkArgs(e, args); err != nil {
		return fail(env, g.human, registry.ErrorOf(err))
	}
	if e.NeedsAgent && agent == "" {
		return fail(env, g.human, &registry.Error{Code: registry.CodeMissingAgent, Message: e.Name + " changes state and needs your identity: pass --agent <name> or set agent in " + cfgPath})
	}
	tr := env.Transport
	if tr == nil {
		tr = &HTTPTransport{Endpoint: endpoint, Timeout: 60 * time.Second}
	}
	out, err := tr.Call(context.Background(), agent, e.Name, args)
	if err != nil {
		return fail(env, g.human, registry.ErrorOf(err))
	}
	if g.human {
		fmt.Fprint(env.Stdout, render(out, 0))
	} else {
		writeRaw(env.Stdout, out)
	}
	return ExitOK
}

// catPage prints a page's Markdown as it is stored: wiki_read with
// content_only, without the JSON around it.
func catPage(env Env, cat Catalog, g globals, path string) int {
	_, endpoint, agent, err := settings(g)
	if err != nil {
		return usageErr(env, g.human, registry.CodeInvalidArguments, "config: "+err.Error())
	}
	if _, ok := cat.Find("wiki_read"); !ok {
		return unknownOp(env, g.human, cat, "wiki_read")
	}
	tr := env.Transport
	if tr == nil {
		tr = &HTTPTransport{Endpoint: endpoint, Timeout: 60 * time.Second}
	}
	out, err := tr.Call(context.Background(), agent, "wiki_read", map[string]any{"path": path, "content_only": true})
	if err != nil {
		return fail(env, g.human, registry.ErrorOf(err))
	}
	var page struct {
		Content *string `json:"content"`
	}
	if err := json.Unmarshal(out, &page); err != nil || page.Content == nil {
		return fail(env, g.human, &registry.Error{Code: registry.CodeBackendError, Message: "wiki_read returned no content"})
	}
	fmt.Fprint(env.Stdout, *page.Content)
	return ExitOK
}

// grepContent runs wiki_grep and prints its passages the way grep -n does:
// "path › heading" over each passage, then "N:" for matching lines and "N-"
// for context. The first argument that is not a flag is the pattern.
func grepContent(env Env, cat Catalog, g globals, rest []string) int {
	e, ok := cat.Find("wiki_grep")
	if !ok {
		return unknownOp(env, g.human, cat, "wiki_grep")
	}
	if g.help {
		fmt.Fprint(env.Stdout, "Usage: den-wiki grep <pattern> [--path P | --folder F | --topic T | --zone Z] [--context N] [--regex] [--case-sensitive] [--per-file N] [--limit N]\n\nPrints wiki_grep's passages as text; wiki_grep --help has the fields.\n")
		return ExitOK
	}
	var flags []string
	pattern := ""
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && i+1 < len(rest) && !strings.HasPrefix(rest[i+1], "-") && !boolFlag(e, a) {
				i++
				flags = append(flags, rest[i])
			}
			continue
		}
		if pattern != "" {
			return usageErr(env, g.human, registry.CodeInvalidArguments, "usage: den-wiki grep <pattern> [flags]; quote a pattern with spaces")
		}
		pattern = a
	}
	if pattern == "" && g.argsJSON == "" {
		return usageErr(env, g.human, registry.CodeInvalidArguments, "usage: den-wiki grep <pattern> [flags]")
	}
	if pattern != "" {
		flags = append([]string{"--pattern", pattern}, flags...)
	}
	_, endpoint, agent, err := settings(g)
	if err != nil {
		return usageErr(env, g.human, registry.CodeInvalidArguments, "config: "+err.Error())
	}
	args, err := buildArgs(e, g, flags, env.Stdin)
	if err != nil {
		return fail(env, g.human, registry.ErrorOf(err))
	}
	if err := checkArgs(e, args); err != nil {
		return fail(env, g.human, registry.ErrorOf(err))
	}
	tr := env.Transport
	if tr == nil {
		tr = &HTTPTransport{Endpoint: endpoint, Timeout: 60 * time.Second}
	}
	out, err := tr.Call(context.Background(), agent, "wiki_grep", args)
	if err != nil {
		return fail(env, g.human, registry.ErrorOf(err))
	}
	var res struct {
		Passages []struct {
			Path    string `json:"path"`
			Heading string `json:"heading"`
			Hits    []int  `json:"hits"`
			Lines   []struct {
				N    int    `json:"n"`
				Text string `json:"text"`
			} `json:"lines"`
		} `json:"passages"`
		Matches   int  `json:"matches"`
		Files     int  `json:"files_matched"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return fail(env, g.human, &registry.Error{Code: registry.CodeBackendError, Message: "wiki_grep returned no passages"})
	}
	for i, p := range res.Passages {
		if i > 0 {
			fmt.Fprintln(env.Stdout)
		}
		head := p.Path
		if p.Heading != "" {
			head += " › " + p.Heading
		}
		fmt.Fprintln(env.Stdout, head)
		hit := map[int]bool{}
		for _, n := range p.Hits {
			hit[n] = true
		}
		for _, l := range p.Lines {
			sep := "-"
			if hit[l.N] {
				sep = ":"
			}
			fmt.Fprintf(env.Stdout, "%d%s%s\n", l.N, sep, l.Text)
		}
	}
	switch {
	case res.Matches == 0:
		fmt.Fprintln(env.Stderr, "no matches")
	case res.Truncated:
		fmt.Fprintf(env.Stderr, "%d matches in %d files shown; more were cut off: narrow the scope or raise --limit / --per-file\n", res.Matches, res.Files)
	}
	return ExitOK
}

// boolFlag reports whether flag names a boolean field of e, which takes no
// value.
func boolFlag(e Entry, flag string) bool {
	props, _ := e.InputSchema["properties"].(map[string]any)
	name := strings.ReplaceAll(strings.TrimLeft(flag, "-"), "-", "_")
	p, _ := props[name].(map[string]any)
	return p["type"] == "boolean"
}

// buildArgs turns field flags, or one --args-json object, into arguments.
func buildArgs(e Entry, g globals, rest []string, stdin io.Reader) (map[string]any, error) {
	props, _ := e.InputSchema["properties"].(map[string]any)
	fields := make([]string, 0, len(props))
	for k := range props {
		fields = append(fields, k)
	}
	sort.Strings(fields)
	args := map[string]any{}
	if g.argsJSON != "" {
		if len(rest) > 0 {
			return nil, &registry.Error{Code: registry.CodeInvalidArguments, Message: "--args-json cannot be mixed with field flags (" + strings.Join(rest, " ") + ")"}
		}
		raw := []byte(g.argsJSON)
		switch {
		case g.argsJSON == "@-":
			b, err := io.ReadAll(stdin)
			if err != nil {
				return nil, err
			}
			raw = b
		case strings.HasPrefix(g.argsJSON, "@"):
			b, err := os.ReadFile(strings.TrimPrefix(g.argsJSON, "@"))
			if err != nil {
				return nil, &registry.Error{Code: registry.CodeInvalidArguments, Message: "--args-json: " + err.Error()}
			}
			raw = b
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&args); err != nil {
			return nil, &registry.Error{Code: registry.CodeInvalidArguments, Message: "--args-json must be one JSON object: " + err.Error()}
		}
		return args, readFileFields(args)
	}
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if !strings.HasPrefix(a, "--") {
			return nil, &registry.Error{Code: registry.CodeInvalidArguments, Message: fmt.Sprintf("unexpected argument %q; give fields as --name value (see den-wiki %s --help)", a, e.Name)}
		}
		name, val, hasVal := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		field := strings.ReplaceAll(name, "-", "_")
		p, ok := props[field].(map[string]any)
		if !ok {
			msg := fmt.Sprintf("%s has no field --%s", e.Name, name)
			if s := registry.Closest(field, fields); s != "" {
				msg += fmt.Sprintf("; did you mean --%s?", s)
			}
			return nil, &registry.Error{Code: registry.CodeUnknownField, Message: msg + " (fields: " + strings.Join(fields, ", ") + ")"}
		}
		typ, _ := p["type"].(string)
		if !hasVal {
			if typ == "boolean" && (i+1 >= len(rest) || strings.HasPrefix(rest[i+1], "--")) {
				val = "true"
			} else {
				if i+1 >= len(rest) {
					return nil, &registry.Error{Code: registry.CodeInvalidArguments, Message: "--" + name + " needs a value"}
				}
				i++
				val = rest[i]
			}
		}
		v, err := flagValue(typ, p, val)
		if err != nil {
			return nil, &registry.Error{Code: registry.CodeInvalidArguments, Message: "--" + name + ": " + err.Error()}
		}
		if typ == "array" {
			// Start from a non-nil slice so an explicit '[]' stays [] and is
			// not sent as null.
			prev, _ := args[field].([]any)
			if prev == nil {
				prev = []any{}
			}
			args[field] = append(prev, v.([]any)...)
			continue
		}
		if _, dup := args[field]; dup {
			return nil, &registry.Error{Code: registry.CodeInvalidArguments, Message: "--" + name + " given twice"}
		}
		args[field] = v
	}
	return args, readFileFields(args)
}

// readFileFields replaces "@<path>" in a *_base64 field with the base64 of
// that local file, so a file can be attached without encoding it by hand.
// Base64 has no "@", so a real value is never mistaken for a path.
func readFileFields(args map[string]any) error {
	for k, v := range args {
		s, ok := v.(string)
		if !ok || !strings.HasSuffix(k, "_base64") || !strings.HasPrefix(s, "@") {
			continue
		}
		b, err := os.ReadFile(strings.TrimPrefix(s, "@"))
		if err != nil {
			return &registry.Error{Code: registry.CodeInvalidArguments, Message: "--" + k + ": " + err.Error()}
		}
		args[k] = base64.StdEncoding.EncodeToString(b)
	}
	return nil
}

func flagValue(typ string, p map[string]any, val string) (any, error) {
	switch typ {
	case "string":
		return val, nil
	case "integer":
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not an integer", val)
		}
		return json.Number(strconv.FormatInt(n, 10)), nil
	case "number":
		if _, err := strconv.ParseFloat(val, 64); err != nil {
			return nil, fmt.Errorf("%q is not a number", val)
		}
		return json.Number(val), nil
	case "boolean":
		b, err := strconv.ParseBool(val)
		if err != nil {
			return nil, fmt.Errorf("%q is not true or false", val)
		}
		return b, nil
	case "array":
		if strings.HasPrefix(strings.TrimSpace(val), "[") {
			var out []any
			dec := json.NewDecoder(strings.NewReader(val))
			dec.UseNumber()
			if err := dec.Decode(&out); err != nil {
				return nil, fmt.Errorf("not a JSON array: %v", err)
			}
			return out, nil
		}
		items, _ := p["items"].(map[string]any)
		it, _ := items["type"].(string)
		var out []any
		for _, part := range strings.Split(val, ",") {
			if part = strings.TrimSpace(part); part == "" {
				continue
			}
			v, err := flagValue(it, items, part)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	}
	// objects and untyped values are JSON
	var out any
	dec := json.NewDecoder(strings.NewReader(val))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("give a JSON value: %v", err)
	}
	return out, nil
}

// checkArgs validates arguments against the operation's schema before any
// request is made.
func checkArgs(e Entry, args map[string]any) error {
	props, _ := e.InputSchema["properties"].(map[string]any)
	fields := make([]string, 0, len(props))
	for k := range props {
		fields = append(fields, k)
	}
	sort.Strings(fields)
	var unknown []string
	for k := range args {
		if _, ok := props[k]; !ok {
			u := fmt.Sprintf("%q", k)
			if s := registry.Closest(k, fields); s != "" {
				u += fmt.Sprintf(" (did you mean %q?)", s)
			}
			unknown = append(unknown, u)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return &registry.Error{Code: registry.CodeUnknownField, Message: fmt.Sprintf("%s does not accept %s; accepted fields: %s", e.Name, strings.Join(unknown, ", "), strings.Join(fields, ", "))}
	}
	raw, _ := json.Marshal(e.InputSchema)
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	c := jsonschema.NewCompiler()
	url := "mem:///" + e.Name + ".json"
	if err := c.AddResource(url, doc); err != nil {
		return err
	}
	sch, err := c.Compile(url)
	if err != nil {
		return err
	}
	inst, _ := json.Marshal(args)
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(inst))
	if err != nil {
		return err
	}
	if err := sch.Validate(v); err != nil {
		var ve *jsonschema.ValidationError
		msg := err.Error()
		if errors.As(err, &ve) {
			msg = leafMessages(ve)
		}
		return &registry.Error{Code: registry.CodeInvalidArguments, Message: e.Name + ": " + msg + " (see den-wiki " + e.Name + " --help)"}
	}
	return nil
}

func leafMessages(ve *jsonschema.ValidationError) string {
	var out []string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			loc := strings.Join(e.InstanceLocation, ".")
			if loc == "" {
				loc = "arguments"
			}
			out = append(out, loc+": "+e.Error())
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	sort.Strings(out)
	return strings.Join(out, "; ")
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func unknownOp(env Env, human bool, cat Catalog, name string) int {
	msg := fmt.Sprintf("unknown operation %q", name)
	if s := registry.Closest(name, cat.Names()); s != "" {
		msg += fmt.Sprintf("; did you mean %q?", s)
	}
	msg += "; den-wiki search <terms> finds operations"
	return fail(env, human, &registry.Error{Code: registry.CodeUnknownOperation, Message: msg})
}

func usageErr(env Env, human bool, code, msg string) int {
	return fail(env, human, &registry.Error{Code: code, Message: msg})
}

// fail writes an error to stderr (JSON envelope, or one line with --human)
// and returns its exit code.
func fail(env Env, human bool, e *registry.Error) int {
	if human {
		fmt.Fprintf(env.Stderr, "error: %s: %s\n", e.Code, e.Message)
	} else {
		writeJSON(env.Stderr, map[string]any{"error": e})
	}
	return ExitFor(e.Code)
}

func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeRaw(w io.Writer, raw json.RawMessage) {
	var b bytes.Buffer
	if json.Indent(&b, raw, "", "  ") != nil {
		_, _ = w.Write(raw)
		fmt.Fprintln(w)
		return
	}
	b.WriteByte('\n')
	_, _ = w.Write(b.Bytes())
}
