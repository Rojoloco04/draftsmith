// Package opgg talks to OP.GG's public MCP server for champion meta data
// (tiers, win/pick/ban rates, counters, builds).
//
// MCP over Streamable HTTP is JSON-RPC 2.0 over POST. Responses come back
// either as plain JSON or as a server-sent event stream, so both are handled.
// No AI agent is involved; this is just a data client.
package opgg

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const DefaultEndpoint = "https://mcp-api.op.gg/mcp"

const protocolVersion = "2025-06-18"

type Client struct {
	Endpoint string
	HTTP     *http.Client

	session string
	nextID  atomic.Int64
	ready   bool
}

func New(endpoint string) *Client {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	return &Client{Endpoint: endpoint, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Tool is one tool advertised by the server.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Init performs the MCP handshake. Called automatically by other methods.
func (c *Client) Init(ctx context.Context) error {
	if c.ready {
		return nil
	}
	_, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "draftsmith", "version": "0.1.0"},
	})
	if err != nil {
		return fmt.Errorf("opgg initialize: %w", err)
	}
	if err := c.notify(ctx, "notifications/initialized"); err != nil {
		return fmt.Errorf("opgg initialized notification: %w", err)
	}
	c.ready = true
	return nil
}

// ListTools returns every tool the server offers, following pagination.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	if err := c.Init(ctx); err != nil {
		return nil, err
	}
	var all []Tool
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var res struct {
			Tools      []Tool `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return nil, err
		}
		all = append(all, res.Tools...)
		if res.NextCursor == "" {
			return all, nil
		}
		cursor = res.NextCursor
	}
}

// CallResult is a tools/call result. Text joins all text content blocks.
type CallResult struct {
	Text              string          `json:"text"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError"`
}

// CallTool invokes a tool with JSON arguments.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (CallResult, error) {
	if err := c.Init(ctx); err != nil {
		return CallResult{}, err
	}
	if args == nil {
		args = map[string]any{}
	}
	raw, err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return CallResult{}, err
	}
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return CallResult{}, err
	}
	var parts []string
	for _, c := range res.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	return CallResult{Text: strings.Join(parts, "\n"), StructuredContent: res.StructuredContent, IsError: res.IsError}, nil
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	resp, err := c.post(ctx, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return nil, fmt.Errorf("opgg %s: HTTP %d: %s", method, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if s := resp.Header.Get("Mcp-Session-Id"); s != "" {
		c.session = s
	}

	var msg *rpcResponse
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		msg, err = readSSE(resp.Body, id)
	} else {
		msg = &rpcResponse{}
		err = json.NewDecoder(resp.Body).Decode(msg)
	}
	if err != nil {
		return nil, fmt.Errorf("opgg %s: %w", method, err)
	}
	if msg.Error != nil {
		return nil, fmt.Errorf("opgg %s: error %d: %s", method, msg.Error.Code, msg.Error.Message)
	}
	return msg.Result, nil
}

func (c *Client) notify(ctx context.Context, method string) error {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method})
	if err != nil {
		return err
	}
	resp, err := c.post(ctx, body)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) post(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.ready || c.session != "" {
		req.Header.Set("MCP-Protocol-Version", protocolVersion)
	}
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
	}
	return c.HTTP.Do(req)
}

// readSSE scans an event stream for the JSON-RPC response with the given id.
func readSSE(r io.Reader, id int64) (*rpcResponse, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)
	var data strings.Builder
	flush := func() (*rpcResponse, bool) {
		defer data.Reset()
		if data.Len() == 0 {
			return nil, false
		}
		var m rpcResponse
		if err := json.Unmarshal([]byte(data.String()), &m); err != nil {
			return nil, false
		}
		if m.ID != nil && *m.ID == id {
			return &m, true
		}
		return nil, false
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if m, ok := flush(); ok {
				return m, nil
			}
			continue
		}
		if v, ok := strings.CutPrefix(line, "data:"); ok {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(v, " "))
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if m, ok := flush(); ok {
		return m, nil
	}
	return nil, fmt.Errorf("no response for request %d in event stream", id)
}
