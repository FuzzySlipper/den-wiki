// Package registry is den-wiki's single operation registry. MCP tools/list
// and the CLI catalog are both generated from it. Each operation's input
// schema is generated from its typed argument struct, and arguments are
// decoded strictly into that same struct, so the schema and the decoder
// cannot drift.
package registry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
)

// Risk classifies what an operation can do.
type Risk string

const (
	RiskRead        Risk = "read"
	RiskWrite       Risk = "write"
	RiskDestructive Risk = "destructive"
)

// Tier says whether an operation is in default discovery or the long tail.
// Long-tail operations are registered and callable by exact name, but only
// listed in the full profile.
type Tier string

const (
	TierDefault  Tier = "default"
	TierLongTail Tier = "long_tail"
)

// Profiles a caller can ask tools/list for.
const (
	ProfileDefault = "default"
	ProfileFull    = "full"
)

// Call is one invocation as a handler sees it.
type Call struct {
	// Agent is the calling agent's identity, from the transport. Empty
	// when the caller sent none.
	Agent string
	// reads collects what a read operation returned or opened, for the
	// read log.
	reads *ReadNote
}

// ReadNote is what a read operation reports for the read log.
type ReadNote struct {
	Query    string   `json:"query,omitempty"`
	Returned []string `json:"returned,omitempty"`
	Opened   []string `json:"opened,omitempty"`
}

// NoteRead records the query and page paths a read returned or opened.
func (c *Call) NoteRead(query string, returned, opened []string) {
	if c.reads == nil {
		return
	}
	c.reads.Query = query
	c.reads.Returned = append(c.reads.Returned, returned...)
	c.reads.Opened = append(c.reads.Opened, opened...)
}

// Example is one documented invocation.
type Example struct {
	Description string         `json:"description"`
	Arguments   map[string]any `json:"arguments"`
}

// Operation is one registered operation.
type Operation struct {
	Name        string
	Description string
	Risk        Risk
	Tier        Tier
	Backend     string
	Aliases     []string
	Examples    []Example
	// NeedsAgent rejects calls without an agent identity.
	NeedsAgent bool

	argsType reflect.Type
	invoke   func(ctx context.Context, call *Call, args any) (any, error)
	schema   map[string]any
	fields   []string
}

// InputSchema returns the operation's JSON Schema (a copy).
func (o *Operation) InputSchema() map[string]any {
	raw, _ := json.Marshal(o.schema)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

// Fields returns the argument field names, sorted.
func (o *Operation) Fields() []string { return append([]string(nil), o.fields...) }

// New builds an operation whose arguments decode into A. A must be a struct;
// see schemaFor for the field tags it understands.
func New[A any](op Operation, fn func(ctx context.Context, call *Call, args A) (any, error)) *Operation {
	var zero A
	t := reflect.TypeOf(zero)
	if t == nil || t.Kind() != reflect.Struct {
		panic("registry: argument type of " + op.Name + " must be a struct")
	}
	schema, fields := schemaFor(t)
	op.argsType = t
	op.schema = schema
	op.fields = fields
	op.invoke = func(ctx context.Context, call *Call, args any) (any, error) {
		return fn(ctx, call, *args.(*A))
	}
	if op.Tier == "" {
		op.Tier = TierLongTail
	}
	return &op
}

// Registry holds every operation.
type Registry struct {
	ops     map[string]*Operation
	aliases map[string]string
	order   []string
}

// NewRegistry builds a registry, rejecting duplicate names and aliases.
func NewRegistry(ops ...*Operation) (*Registry, error) {
	r := &Registry{ops: map[string]*Operation{}, aliases: map[string]string{}}
	for _, op := range ops {
		if !nameRe(op.Name) {
			return nil, fmt.Errorf("operation name %q must be lowercase words joined by _", op.Name)
		}
		if _, dup := r.ops[op.Name]; dup {
			return nil, fmt.Errorf("duplicate operation %q", op.Name)
		}
		if op.Description == "" || op.Risk == "" || op.Backend == "" {
			return nil, fmt.Errorf("operation %q needs a description, risk and backend", op.Name)
		}
		if op.Risk != RiskRead {
			op.NeedsAgent = true
		}
		r.ops[op.Name] = op
		r.order = append(r.order, op.Name)
	}
	for _, op := range ops {
		for _, a := range op.Aliases {
			if _, clash := r.ops[a]; clash {
				return nil, fmt.Errorf("alias %q of %q is also an operation", a, op.Name)
			}
			if prev, dup := r.aliases[a]; dup {
				return nil, fmt.Errorf("alias %q names both %q and %q", a, prev, op.Name)
			}
			r.aliases[a] = op.Name
		}
	}
	sort.Strings(r.order)
	return r, nil
}

func nameRe(s string) bool {
	if s == "" || s[0] == '_' || s[len(s)-1] == '_' || strings.Contains(s, "__") {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

// All returns every operation, sorted by name.
func (r *Registry) All() []*Operation {
	out := make([]*Operation, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.ops[n])
	}
	return out
}

// Listed returns the operations a profile lists.
func (r *Registry) Listed(profile string) ([]*Operation, error) {
	switch profile {
	case "", ProfileDefault:
		var out []*Operation
		for _, op := range r.All() {
			if op.Tier == TierDefault {
				out = append(out, op)
			}
		}
		return out, nil
	case ProfileFull:
		return r.All(), nil
	}
	return nil, fmt.Errorf("unknown tool profile %q (want %s or %s)", profile, ProfileDefault, ProfileFull)
}

// Resolve finds an operation by name or alias.
func (r *Registry) Resolve(name string) (*Operation, error) {
	if op, ok := r.ops[name]; ok {
		return op, nil
	}
	if canon, ok := r.aliases[name]; ok {
		return r.ops[canon], nil
	}
	msg := fmt.Sprintf("unknown operation %q", name)
	if s := closest(name, r.order); s != "" {
		msg += fmt.Sprintf("; did you mean %q?", s)
	}
	return nil, &Error{Code: CodeUnknownOperation, Message: msg}
}

// Revision identifies the registered surface: it changes whenever an
// operation, alias, tier or schema changes.
func (r *Registry) Revision() string {
	h := sha256.New()
	for _, op := range r.All() {
		raw, _ := json.Marshal(struct {
			N, D    string
			R       Risk
			T       Tier
			A       []string
			S       map[string]any
			NeedsAg bool
		}{op.Name, op.Description, op.Risk, op.Tier, op.Aliases, op.schema, op.NeedsAgent})
		h.Write(raw)
	}
	return "catalog-" + hex.EncodeToString(h.Sum(nil))[:12]
}

// Result is what Invoke returns besides the operation's own value.
type Result struct {
	Value     any
	Canonical string
	Reads     *ReadNote
}

// Invoke resolves, strictly decodes and runs one call. Unknown fields,
// missing required fields and a missing agent identity are rejected before
// the handler (and so any backend) runs.
func (r *Registry) Invoke(ctx context.Context, name, agent string, raw json.RawMessage) (Result, error) {
	op, err := r.Resolve(name)
	if err != nil {
		return Result{}, err
	}
	res := Result{Canonical: op.Name}
	if op.NeedsAgent && strings.TrimSpace(agent) == "" {
		return res, &Error{Code: CodeMissingAgent, Message: op.Name + " changes state and needs the calling agent's identity; set it on the client (MCP header X-Den-Wiki-Agent, CLI --agent or config agent)"}
	}
	args, err := op.decode(raw)
	if err != nil {
		return res, err
	}
	call := &Call{Agent: strings.TrimSpace(agent)}
	if op.Risk == RiskRead {
		call.reads = &ReadNote{}
	}
	v, err := op.invoke(ctx, call, args)
	res.Value, res.Reads = v, call.reads
	return res, err
}

// Decode strictly decodes raw into the operation's argument struct and
// checks required fields; it is exported for the CLI's flag parsing.
func (o *Operation) Decode(raw json.RawMessage) error {
	_, err := o.decode(raw)
	return err
}

func (o *Operation) decode(raw json.RawMessage) (any, error) {
	args := reflect.New(o.argsType).Interface()
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		raw = []byte("{}")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, &Error{Code: CodeInvalidArguments, Message: "arguments must be a JSON object: " + err.Error()}
	}
	known := map[string]bool{}
	for _, f := range o.fields {
		known[f] = true
	}
	var unknown []string
	for k := range probe {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		parts := make([]string, 0, len(unknown))
		for _, k := range unknown {
			p := fmt.Sprintf("%q", k)
			if s := closest(k, o.fields); s != "" {
				p += fmt.Sprintf(" (did you mean %q?)", s)
			}
			parts = append(parts, p)
		}
		return nil, &Error{Code: CodeUnknownField, Message: fmt.Sprintf("%s does not accept %s; accepted fields: %s", o.Name, strings.Join(parts, ", "), strings.Join(o.fields, ", "))}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(args); err != nil {
		return nil, &Error{Code: CodeInvalidArguments, Message: o.Name + ": " + err.Error()}
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, &Error{Code: CodeInvalidArguments, Message: o.Name + ": arguments must be a single JSON object"}
	}
	req, _ := o.schema["required"].([]string)
	var missing []string
	for _, f := range req {
		if _, ok := probe[f]; !ok {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		return nil, &Error{Code: CodeInvalidArguments, Message: fmt.Sprintf("%s requires %s", o.Name, strings.Join(missing, ", "))}
	}
	return args, nil
}

// Closest returns the candidate within edit distance 3 of s, or "". Used
// for "did you mean" suggestions.
func Closest(s string, candidates []string) string { return closest(s, candidates) }

func closest(s string, candidates []string) string {
	best, bestD := "", 4
	for _, c := range candidates {
		if d := editDistance(s, c); d < bestD {
			best, bestD = c, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
