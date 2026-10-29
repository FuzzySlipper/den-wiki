// Package okclient talks to an OpenKnowledge server over its MCP endpoint
// (streamable HTTP). den-wiki never links OK code: OK is GPL-3 and is
// reached only over HTTP.
package okclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Errors a caller can switch on.
var (
	// ErrUnavailable means OK could not be reached or answered with a
	// transport-level failure. Calls are safe to retry.
	ErrUnavailable = errors.New("openknowledge unavailable")
)

// ToolError is an error result from an OK tool (isError true).
type ToolError struct {
	Tool    string
	Message string
	// Structured is OK's structuredContent, when it sent one.
	Structured json.RawMessage
}

func (e *ToolError) Error() string { return "openknowledge " + e.Tool + ": " + e.Message }

// Tool is one OK tools/list entry.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Client is an MCP client for one OK server. It keeps one MCP session per
// client identity, because OK attributes writes to the session's client.
type Client struct {
	url     string
	http    *http.Client
	nextID  atomic.Int64
	mu      sync.Mutex
	session map[string]string // identity -> Mcp-Session-Id
	// Identify, when set, adds per-agent identity to every request for
	// identity (headers, clientInfo). See the attribution notes in
	// docs/facade.md.
	Identify func(identity string, h http.Header)
}

// New builds a client for OK's MCP endpoint URL.
func New(url string, timeout time.Duration) *Client {
	return &Client{url: url, http: &http.Client{Timeout: timeout}, session: map[string]string{}}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// ClientName is the MCP clientInfo name den-wiki sends for identity.
func ClientName(identity string) string {
	if identity == "" {
		return "den-wiki"
	}
	return "den-wiki/" + identity
}

func (c *Client) post(ctx context.Context, identity string, req rpcRequest) (json.RawMessage, http.Header, error) {
	body, _ := json.Marshal(req)
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Accept", "application/json, text/event-stream")
	hr.Header.Set("MCP-Protocol-Version", "2025-06-18")
	c.mu.Lock()
	sid := c.session[identity]
	c.mu.Unlock()
	if sid != "" && req.Method != "initialize" {
		hr.Header.Set("Mcp-Session-Id", sid)
	}
	if c.Identify != nil {
		c.Identify(identity, hr.Header)
	}
	resp, err := c.http.Do(hr)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound && sid != "" {
		return nil, resp.Header, errSessionGone
	}
	if resp.StatusCode == http.StatusAccepted {
		return nil, resp.Header, nil
	}
	if resp.StatusCode >= 500 {
		return nil, nil, fmt.Errorf("%w: HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, nil, fmt.Errorf("openknowledge HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if req.ID == 0 {
		return nil, resp.Header, nil
	}
	msg, err := readMessage(resp, req.ID)
	if err != nil {
		return nil, nil, err
	}
	var rr rpcResponse
	if err := json.Unmarshal(msg, &rr); err != nil {
		return nil, nil, fmt.Errorf("openknowledge sent invalid JSON-RPC: %w", err)
	}
	if rr.Error != nil {
		return nil, resp.Header, fmt.Errorf("openknowledge %s: %s (code %d)", req.Method, rr.Error.Message, rr.Error.Code)
	}
	return rr.Result, resp.Header, nil
}

var errSessionGone = errors.New("mcp session expired")

// readMessage returns the JSON-RPC response with id from a plain JSON body
// or an SSE stream.
func readMessage(resp *http.Response, id int64) (json.RawMessage, error) {
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		if err != nil {
			return nil, fmt.Errorf("%w: reading response: %v", ErrUnavailable, err)
		}
		return raw, nil
	}
	want := fmt.Sprint(id)
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	var data strings.Builder
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		case line == "":
			if data.Len() > 0 {
				msg := json.RawMessage(data.String())
				data.Reset()
				var probe struct {
					ID json.RawMessage `json:"id"`
				}
				if json.Unmarshal(msg, &probe) == nil && string(probe.ID) == want {
					return msg, nil
				}
			}
		}
	}
	if data.Len() > 0 {
		return json.RawMessage(data.String()), nil
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: reading event stream: %v", ErrUnavailable, err)
	}
	return nil, fmt.Errorf("%w: event stream ended without a response", ErrUnavailable)
}

// ensureSession initialises an MCP session for identity if there is none.
func (c *Client) ensureSession(ctx context.Context, identity string) error {
	c.mu.Lock()
	_, ok := c.session[identity]
	c.mu.Unlock()
	if ok {
		return nil
	}
	_, hdr, err := c.post(ctx, identity, rpcRequest{
		JSONRPC: "2.0", ID: c.nextID.Add(1), Method: "initialize",
		Params: map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": ClientName(identity), "version": "1"},
		},
	})
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.session[identity] = hdr.Get("Mcp-Session-Id")
	c.mu.Unlock()
	_, _, err = c.post(ctx, identity, rpcRequest{JSONRPC: "2.0", Method: "notifications/initialized"})
	return err
}

// rpc runs one request in identity's session, re-initialising once if OK
// has dropped the session.
func (c *Client) rpc(ctx context.Context, identity, method string, params any) (json.RawMessage, error) {
	for attempt := 0; ; attempt++ {
		if err := c.ensureSession(ctx, identity); err != nil {
			return nil, err
		}
		res, _, err := c.post(ctx, identity, rpcRequest{JSONRPC: "2.0", ID: c.nextID.Add(1), Method: method, Params: params})
		if errors.Is(err, errSessionGone) && attempt == 0 {
			c.mu.Lock()
			delete(c.session, identity)
			c.mu.Unlock()
			continue
		}
		return res, err
	}
}

// Ping checks that OK answers.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.rpc(ctx, "", "ping", map[string]any{})
	return err
}

// ListTools returns OK's tools.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	res, err := c.rpc(ctx, "", "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, fmt.Errorf("decode OK tools/list: %w", err)
	}
	return out.Tools, nil
}

// CallResult is a successful OK tool result.
type CallResult struct {
	// Text is the concatenated text content.
	Text string
	// Structured is structuredContent when OK sent it, else the Text
	// parsed as JSON when it is JSON, else nil.
	Structured json.RawMessage
}

// Call runs one OK tool as identity.
func (c *Client) Call(ctx context.Context, identity, tool string, args any) (CallResult, error) {
	res, err := c.rpc(ctx, identity, "tools/call", map[string]any{"name": tool, "arguments": args})
	if err != nil {
		return CallResult{}, err
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError           bool            `json:"isError"`
		StructuredContent json.RawMessage `json:"structuredContent"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return CallResult{}, fmt.Errorf("decode OK %s result: %w", tool, err)
	}
	var text strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			if text.Len() > 0 {
				text.WriteString("\n")
			}
			text.WriteString(c.Text)
		}
	}
	if out.IsError {
		return CallResult{}, &ToolError{Tool: tool, Message: text.String(), Structured: out.StructuredContent}
	}
	cr := CallResult{Text: text.String(), Structured: out.StructuredContent}
	if len(cr.Structured) == 0 && json.Valid([]byte(cr.Text)) {
		cr.Structured = json.RawMessage(cr.Text)
	}
	return cr, nil
}
