// Package facade is den-wiki's one MCP endpoint. It owns tool schemas and
// descriptions, discovery profiles, the catalog revision, aliases and
// routing; state stays in the ledger and in the content repository behind
// OpenKnowledge.
package facade

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/FuzzySlipper/den-wiki/internal/registry"
)

// Request headers. The agent header carries the calling agent's identity;
// the profile header picks the discovery profile.
const (
	AgentHeader   = "X-Den-Wiki-Agent"
	ProfileHeader = "X-Den-Wiki-Tool-Profile"
)

const (
	jsonRPCVersion = "2.0"
	latestProtocol = "2025-06-18"

	errParse          = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternal       = -32603

	// maxMCPBody fits one wiki_attach of MaxAttachBytes, base64 encoded.
	maxMCPBody = MaxAttachBytes/3*4 + 1<<20
)

// MCP serves JSON-RPC MCP over plain HTTP POST (stateless; no sessions).
type MCP struct {
	reg     *registry.Registry
	reads   ReadLog
	logger  *slog.Logger
	clock   func() time.Time
	version string
}

// NewMCP builds the MCP handler.
func NewMCP(reg *registry.Registry, reads ReadLog, logger *slog.Logger, clock func() time.Time, version string) *MCP {
	return &MCP{reg: reg, reads: reads, logger: logger, clock: clock, version: version}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ListedTool is one tools/list entry.
type ListedTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations"`
}

// Catalog describes the registered surface in every tools/list and
// initialize result.
type Catalog struct {
	Revision string `json:"revision"`
	Profile  string `json:"profile"`
	Listed   int    `json:"listed"`
	Hidden   int    `json:"hidden"`
	Version  string `json:"version"`
}

type toolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	// Meta is MCP's request metadata (progress tokens and the like), which
	// clients such as Codex send on every call. It is accepted and ignored;
	// the arguments themselves are still decoded strictly.
	Meta json.RawMessage `json:"_meta,omitempty"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolsCallResult struct {
	Content           []textContent `json:"content"`
	IsError           bool          `json:"isError"`
	StructuredContent any           `json:"structuredContent,omitempty"`
}

type requestInfo struct {
	agent, profile string
}

func (m *MCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "the den-wiki MCP endpoint accepts POST only", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxMCPBody))
	if err != nil {
		writeRPC(w, rpcErr(nil, errInternal, "reading request body"))
		return
	}
	info := requestInfo{agent: strings.TrimSpace(r.Header.Get(AgentHeader)), profile: strings.TrimSpace(r.Header.Get(ProfileHeader))}
	body = bytes.TrimSpace(body)
	if len(body) > 0 && body[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(body, &batch); err != nil || len(batch) == 0 {
			writeRPC(w, rpcErr(nil, errInvalidRequest, "invalid JSON-RPC batch"))
			return
		}
		var out []rpcResponse
		for _, raw := range batch {
			if resp, ok := m.handle(r.Context(), info, raw); ok {
				out = append(out, resp)
			}
		}
		if len(out) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeRPC(w, out)
		return
	}
	resp, ok := m.handle(r.Context(), info, body)
	if !ok {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	writeRPC(w, resp)
}

func (m *MCP) handle(ctx context.Context, info requestInfo, raw []byte) (rpcResponse, bool) {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return rpcErr(nil, errParse, "parse error"), true
	}
	if req.ID == nil {
		return rpcResponse{}, false // notification
	}
	if req.JSONRPC != jsonRPCVersion || req.Method == "" {
		return rpcErr(req.ID, errInvalidRequest, "invalid JSON-RPC request"), true
	}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		cat, err := m.catalog(info.profile)
		if err != nil {
			return rpcErr(req.ID, errInvalidParams, err.Error()), true
		}
		return rpcOK(req.ID, map[string]any{
			"protocolVersion": negotiate(p.ProtocolVersion),
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "den-wiki", "title": "den-wiki", "version": m.version},
			"instructions": "den-wiki research knowledge base. Search and read before writing; one concept, one page. " +
				"Research runs follow the den-wiki-research skill (ledger job, trail, sources, pages); read-only lookups follow den-wiki-lookup. " +
				"Send your identity in the " + AgentHeader + " header. Long-tail tools are callable by exact name; list them with the " + ProfileHeader + ": full header.",
			"catalog": cat,
		}), true
	case "ping":
		return rpcOK(req.ID, map[string]any{}), true
	case "tools/list":
		var p struct {
			ToolProfile string `json:"toolProfile"`
		}
		_ = json.Unmarshal(req.Params, &p)
		profile := info.profile
		if p.ToolProfile != "" {
			profile = p.ToolProfile
		}
		ops, err := m.reg.Listed(profile)
		if err != nil {
			return rpcErr(req.ID, errInvalidParams, err.Error()), true
		}
		tools := make([]ListedTool, 0, len(ops))
		for _, op := range ops {
			tools = append(tools, listed(op))
		}
		cat, _ := m.catalog(profile)
		return rpcOK(req.ID, map[string]any{"tools": tools, "catalog": cat}), true
	case "tools/call":
		return m.toolsCall(ctx, info, req), true
	}
	return rpcErr(req.ID, errMethodNotFound, "method not found: "+req.Method), true
}

func listed(op *registry.Operation) ListedTool {
	return ListedTool{
		Name: op.Name, Description: op.Description, InputSchema: op.InputSchema(),
		Annotations: map[string]any{
			"readOnlyHint":    op.Risk == registry.RiskRead,
			"destructiveHint": op.Risk == registry.RiskDestructive,
			"tier":            string(op.Tier),
		},
	}
}

func (m *MCP) catalog(profile string) (Catalog, error) {
	if profile == "" {
		profile = registry.ProfileDefault
	}
	ops, err := m.reg.Listed(profile)
	if err != nil {
		return Catalog{}, err
	}
	all := len(m.reg.All())
	return Catalog{Revision: m.reg.Revision(), Profile: profile, Listed: len(ops), Hidden: all - len(ops), Version: m.version}, nil
}

// toolsCall runs one tool and logs one privacy-safe telemetry line: no
// arguments, content or backend error bodies.
func (m *MCP) toolsCall(ctx context.Context, info requestInfo, req rpcRequest) rpcResponse {
	start := m.clock()
	var p toolsCallParams
	requested, canonical, outcome, retryable := "", "", "invalid_params", false
	defer func() {
		m.logger.Info("mcp_tool_call",
			"requested_tool", requested, "canonical_tool", canonical, "agent", info.agent,
			"outcome", outcome, "retryable", retryable, "duration_ms", m.clock().Sub(start).Milliseconds())
	}()
	dec := json.NewDecoder(bytes.NewReader(req.Params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil || p.Name == "" {
		return rpcErr(req.ID, errInvalidParams, "tools/call params must be {name, arguments}")
	}
	requested = p.Name
	res, err := m.reg.Invoke(ctx, p.Name, info.agent, p.Arguments)
	canonical = res.Canonical
	if err != nil {
		e := registry.ErrorOf(err)
		outcome, retryable = e.Code, e.Retryable
		if e.Code == registry.CodeUnknownOperation {
			return rpcErr(req.ID, errInvalidParams, e.Message)
		}
		return rpcOK(req.ID, toolsCallResult{
			Content: []textContent{{Type: "text", Text: e.Code + ": " + e.Message}}, IsError: true,
			StructuredContent: map[string]any{"error": e},
		})
	}
	outcome = "success"
	if res.Reads != nil && m.reads != nil {
		entry := ReadEntry{At: m.clock(), Agent: info.agent, Tool: res.Canonical, Query: res.Reads.Query, Returned: res.Reads.Returned, Opened: res.Reads.Opened}
		if err := m.reads.Record(ctx, entry); err != nil {
			m.logger.Warn("read_log_failed", "tool", res.Canonical, "error", err.Error())
		}
	}
	text, err := json.Marshal(res.Value)
	if err != nil {
		outcome = registry.CodeInternal
		return rpcErr(req.ID, errInternal, fmt.Sprintf("encode %s result: %v", res.Canonical, err))
	}
	var structured any = res.Value
	if _, isObject := asObject(text); !isObject {
		structured = map[string]any{"result": res.Value}
	}
	return rpcOK(req.ID, toolsCallResult{Content: []textContent{{Type: "text", Text: string(text)}}, StructuredContent: structured})
}

func asObject(raw []byte) (map[string]any, bool) {
	var m map[string]any
	return m, json.Unmarshal(raw, &m) == nil && m != nil
}

func negotiate(requested string) string {
	for _, v := range []string{latestProtocol, "2025-03-26", "2024-11-05"} {
		if requested == v {
			return v
		}
	}
	return latestProtocol
}

func rpcOK(id json.RawMessage, result any) rpcResponse {
	return rpcResponse{JSONRPC: jsonRPCVersion, ID: id, Result: result}
}

func rpcErr(id json.RawMessage, code int, msg string) rpcResponse {
	return rpcResponse{JSONRPC: jsonRPCVersion, ID: id, Error: &rpcError{Code: code, Message: msg}}
}

func writeRPC(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
