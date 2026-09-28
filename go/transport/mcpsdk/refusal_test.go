package mcpsdk

import (
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hop.top/kit/go/transport/cmdsurface"
)

// A call the rate limit refuses is an isError result whose text starts
// with the code, with the code and retry hint in _meta.
func TestRateLimitedCallIsARefusalResult(t *testing.T) {
	srv, _, _ := newSurfaceHarness(t, newTestTree(), []cmdsurface.Option{
		cmdsurface.WithRateLimit(cmdsurface.RateLimit{Read: cmdsurface.RateRule{PerMinute: 1, Burst: 1}}),
	})
	sess := connect(t, srv.URL+"/mcp", nil)

	call := func() *mcp.CallToolResult {
		res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "ping"})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		return res
	}
	if res := call(); res.IsError {
		t.Fatalf("first call refused: %s", textOf(res))
	}
	res := call()
	if !res.IsError || !strings.HasPrefix(textOf(res), "rate_limited: ") {
		t.Fatalf("isError=%t text=%q, want a rate_limited refusal", res.IsError, textOf(res))
	}
	refusal, _ := res.Meta[cmdsurface.MCPRefusalMetaKey].(map[string]any)
	if refusal["code"] != "rate_limited" {
		t.Fatalf("_meta = %v, want the refusal code", res.Meta)
	}
	if ms, _ := refusal["retry_after_ms"].(float64); ms <= 55_000 || ms > 60_000 {
		t.Errorf("retry_after_ms = %v, want just under a minute", refusal["retry_after_ms"])
	}
}

func TestOtherRefusalsKeepTheirMessage(t *testing.T) {
	res := refusalResult(cmdsurface.ErrPermissionDenied)
	if textOf(res) != cmdsurface.ErrPermissionDenied.Error() || res.Meta != nil {
		t.Fatalf("result = %q %v", textOf(res), res.Meta)
	}
}

func TestOverloadedCallIsARefusalResult(t *testing.T) {
	res := refusalResult(&cmdsurface.OverloadedError{Path: "ping", MaxInflight: 1, RetryAfter: 1500 * time.Millisecond})
	if !res.IsError || !strings.HasPrefix(textOf(res), "overloaded: ") {
		t.Fatalf("isError=%t text=%q, want an overloaded refusal", res.IsError, textOf(res))
	}
	refusal, _ := res.Meta[cmdsurface.MCPRefusalMetaKey].(map[string]any)
	if refusal["code"] != cmdsurface.CodeOverloaded || refusal["retry_after_ms"] != int64(1500) {
		t.Fatalf("_meta = %v, want the code and retry hint", res.Meta)
	}
}
