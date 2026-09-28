package cmdsurface

import (
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// oneAMinute allows one call a minute in every tier.
var oneAMinute = RateLimit{
	Read:        RateRule{PerMinute: 1, Burst: 1},
	Write:       RateRule{PerMinute: 1, Burst: 1},
	Destructive: RateRule{PerMinute: 1, Burst: 1},
}

// assertMCPRefusal checks a tools/call result is the rate_limited
// refusal: isError, text starting with the code, and the code and
// retry hint in _meta["hop.top/refusal"].
func assertMCPRefusal(t *testing.T, res map[string]any) {
	t.Helper()
	if res["isError"] != true {
		t.Fatalf("isError = %v, want true: %v", res["isError"], res)
	}
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("no content: %v", res)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	if !strings.HasPrefix(text, "rate_limited: ") {
		t.Errorf("text = %q, want it to start with the code", text)
	}
	meta, _ := res["_meta"].(map[string]any)
	refusal, _ := meta[MCPRefusalMetaKey].(map[string]any)
	if refusal["code"] != "rate_limited" {
		t.Fatalf("_meta refusal = %v, want code rate_limited", meta)
	}
	ms, _ := refusal["retry_after_ms"].(float64)
	if ms <= 55_000 || ms > 60_000 {
		t.Errorf("retry_after_ms = %v, want just under a minute", refusal["retry_after_ms"])
	}
}

func TestRateLimit_MCPLegacy(t *testing.T) {
	srv := newMCPHarness(t, func(root *cobra.Command) (*Bridge, error) {
		return New(root, WithRunner(okRunner()), WithRateLimit(oneAMinute)), nil
	})
	call := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "ping"},
	}
	if _, resp := postRPC(t, srv, nil, call); resp.Error != nil || resultAsMap(t, resp)["isError"] != false {
		t.Fatalf("first call: %+v", resp)
	}
	status, resp := postRPC(t, srv, nil, call)
	if status != http.StatusOK || resp.Error != nil {
		t.Fatalf("refusal is a result at 200: status %d, error %+v", status, resp.Error)
	}
	assertMCPRefusal(t, resultAsMap(t, resp))
}

func TestRateLimit_MCPModern(t *testing.T) {
	srv := modernServerFor(t, New(modernTestTree(), WithRateLimit(oneAMinute)))
	post := func() (int, map[string]any) {
		return postJSON(t, srv, "/mcp", modernHeaders("tools/call", "ping"), callBody(t, "ping", nil))
	}
	if status, m := post(); status != http.StatusOK || m["result"].(map[string]any)["isError"] != false {
		t.Fatalf("first call: %d %v", status, m)
	}
	status, m := post()
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %v", status, m)
	}
	res, _ := m["result"].(map[string]any)
	assertMCPRefusal(t, res)
	// The refusal's _meta entry sits beside the server's own.
	if _, ok := res["_meta"].(map[string]any)[metaKeyServerInfo]; !ok {
		t.Errorf("serverInfo dropped from _meta: %v", res["_meta"])
	}
}
