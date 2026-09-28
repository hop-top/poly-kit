package cmdsurface

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// oneSlot is a capacity gate with one slot and no queue.
var oneSlot = Concurrency{MaxInflight: 1}

// assertMCPOverloaded checks a tools/call result is the overloaded
// refusal: isError, text starting with the code, and the code and
// retry hint in _meta["hop.top/refusal"].
func assertMCPOverloaded(t *testing.T, res map[string]any) {
	t.Helper()
	if res["isError"] != true {
		t.Fatalf("isError = %v, want true: %v", res["isError"], res)
	}
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("no content: %v", res)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	if !strings.HasPrefix(text, "overloaded: ") {
		t.Errorf("text = %q, want it to start with the code", text)
	}
	meta, _ := res["_meta"].(map[string]any)
	refusal, _ := meta[MCPRefusalMetaKey].(map[string]any)
	if refusal["code"] != CodeOverloaded {
		t.Fatalf("_meta refusal = %v, want code overloaded", meta)
	}
	if ms, _ := refusal["retry_after_ms"].(float64); ms != 1000 {
		t.Errorf("retry_after_ms = %v, want 1000", refusal["retry_after_ms"])
	}
}

func TestCapacity_MCPLegacy(t *testing.T) {
	srv := newMCPHarness(t, func(root *cobra.Command) (*Bridge, error) {
		b := New(root, WithRunner(okRunner()), WithConcurrency(oneSlot))
		t.Cleanup(TestingHoldSlots(b))
		return b, nil
	})
	call := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "ping"},
	}
	status, resp := postRPC(t, srv, nil, call)
	if status != http.StatusOK || resp.Error != nil {
		t.Fatalf("refusal is a result at 200: status %d, error %+v", status, resp.Error)
	}
	assertMCPOverloaded(t, resultAsMap(t, resp))
}

func TestCapacity_MCPModern(t *testing.T) {
	b := New(modernTestTree(), WithConcurrency(oneSlot))
	defer TestingHoldSlots(b)()
	srv := modernServerFor(t, b)
	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "ping"), callBody(t, "ping", nil))
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %v", status, m)
	}
	res, _ := m["result"].(map[string]any)
	assertMCPOverloaded(t, res)
	if _, ok := res["_meta"].(map[string]any)[metaKeyServerInfo]; !ok {
		t.Errorf("serverInfo dropped from _meta: %v", res["_meta"])
	}
}

func TestMCPRefusal_Overloaded(t *testing.T) {
	err := &OverloadedError{Path: "ping", Surface: SurfaceMCP, MaxInflight: 1, RetryAfter: 2500 * time.Millisecond}
	text, refusal, ok := MCPRefusal(err)
	if !ok || !strings.HasPrefix(text, "overloaded: ") {
		t.Fatalf("MCPRefusal = %q, %v", text, ok)
	}
	if refusal["code"] != CodeOverloaded || refusal["retry_after_ms"] != int64(2500) {
		t.Fatalf("refusal = %v", refusal)
	}
}
