package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/FuzzySlipper/den-wiki/internal/facade"
	"github.com/FuzzySlipper/den-wiki/internal/registry"
)

// Transport calls one operation and returns its JSON result.
type Transport interface {
	Call(ctx context.Context, agent, op string, args map[string]any) (json.RawMessage, error)
}

// HTTPTransport calls the den-wiki facade over its MCP endpoint, sending the
// agent identity in the X-Den-Wiki-Agent header.
type HTTPTransport struct {
	Endpoint string
	Timeout  time.Duration
}

func (t *HTTPTransport) Call(ctx context.Context, agent, op string, args map[string]any) (json.RawMessage, error) {
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": op, "arguments": args},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, &registry.Error{Code: registry.CodeInvalidArguments, Message: fmt.Sprintf("endpoint %q: %v", t.Endpoint, err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if agent != "" {
		req.Header.Set(facade.AgentHeader, agent)
	}
	client := &http.Client{Timeout: t.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &registry.Error{Code: registry.CodeBackendUnavailable, Retryable: true, Message: fmt.Sprintf("cannot reach den-wiki at %s: %v; check --endpoint or endpoint in %s", t.Endpoint, err, DefaultConfigPath())}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, &registry.Error{Code: registry.CodeBackendUnavailable, Retryable: true, Message: "reading den-wiki response: " + err.Error()}
	}
	if resp.StatusCode != http.StatusOK {
		code := registry.CodeInternal
		if resp.StatusCode >= 500 {
			code = registry.CodeBackendUnavailable
		}
		return nil, &registry.Error{Code: code, Message: fmt.Sprintf("den-wiki at %s answered HTTP %d: %s", t.Endpoint, resp.StatusCode, strings.TrimSpace(string(raw)))}
	}
	return decodeToolResponse(raw)
}

// decodeToolResponse turns a tools/call JSON-RPC response into the result or
// a coded error.
func decodeToolResponse(raw []byte) (json.RawMessage, error) {
	var rr struct {
		Result *struct {
			IsError           bool            `json:"isError"`
			StructuredContent json.RawMessage `json:"structuredContent"`
			Content           []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &rr); err != nil {
		return nil, &registry.Error{Code: registry.CodeInternal, Message: "den-wiki sent invalid JSON-RPC: " + err.Error()}
	}
	if rr.Error != nil {
		code := registry.CodeInternal
		if rr.Error.Code == -32602 {
			code = registry.CodeUnknownOperation
		}
		return nil, &registry.Error{Code: code, Message: rr.Error.Message}
	}
	if rr.Result == nil {
		return nil, &registry.Error{Code: registry.CodeInternal, Message: "den-wiki sent no result"}
	}
	if rr.Result.IsError {
		var env struct {
			Error *registry.Error `json:"error"`
		}
		if json.Unmarshal(rr.Result.StructuredContent, &env) == nil && env.Error != nil {
			return nil, env.Error
		}
		msg := "operation failed"
		if len(rr.Result.Content) > 0 {
			msg = rr.Result.Content[0].Text
		}
		return nil, &registry.Error{Code: registry.CodeInternal, Message: msg}
	}
	if len(rr.Result.StructuredContent) > 0 {
		var wrapped struct {
			Result json.RawMessage `json:"result"`
		}
		// Non-object results arrive wrapped as {"result": ...}.
		if json.Unmarshal(rr.Result.StructuredContent, &wrapped) == nil && len(wrapped.Result) > 0 && countKeys(rr.Result.StructuredContent) == 1 {
			return wrapped.Result, nil
		}
		return rr.Result.StructuredContent, nil
	}
	if len(rr.Result.Content) > 0 {
		return json.RawMessage(rr.Result.Content[0].Text), nil
	}
	return json.RawMessage("null"), nil
}

func countKeys(raw json.RawMessage) int {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return -1
	}
	return len(m)
}
