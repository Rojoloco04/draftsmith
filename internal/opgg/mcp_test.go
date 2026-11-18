package opgg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeServer mimics a Streamable HTTP MCP server: JSON for initialize, SSE
// for tools/list and tools/call, 202 for notifications.
func fakeServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		json.Unmarshal(body, &req)

		if req.Method != "initialize" && r.Header.Get("Mcp-Session-Id") != "sess-1" {
			t.Errorf("%s sent without session id", req.Method)
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess-1")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"fake"}}}`, *req.ID)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n")
			fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"tools\":[{\"name\":\"lol_list_lane_meta_champions\",\"description\":\"Lane tiers\",\"inputSchema\":{\"type\":\"object\"}}]}}\n\n", *req.ID)
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			json.Unmarshal(req.Params, &p)
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"%s lane=%v\"}]}}\n\n", *req.ID, p.Name, p.Arguments["position"])
		default:
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32601,"message":"no such method"}}`, *req.ID)
		}
	}))
}

func TestListAndCall(t *testing.T) {
	srv := fakeServer(t)
	defer srv.Close()
	c := New(srv.URL)
	ctx := context.Background()

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "lol_list_lane_meta_champions" {
		t.Fatalf("tools = %+v", tools)
	}
	res, err := c.CallTool(ctx, "lol_list_lane_meta_champions", map[string]any{"position": "mid"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "lol_list_lane_meta_champions lane=mid" {
		t.Fatalf("text = %q", res.Text)
	}
}

func TestRPCError(t *testing.T) {
	srv := fakeServer(t)
	defer srv.Close()
	c := New(srv.URL)
	if err := c.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.call(context.Background(), "bogus", nil); err == nil {
		t.Fatal("expected error")
	}
}
