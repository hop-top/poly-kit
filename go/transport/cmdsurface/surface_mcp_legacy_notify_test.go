package cmdsurface

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
)

// legacyMounts covers both shapes that reach the legacy handler: the
// default dual-era mount (through the dispatcher) and a legacy-only
// mount (handler mounted directly).
var legacyMounts = map[string][]MCPOption{
	"dual-era":    nil,
	"legacy-only": {WithMCPSpecVersions(MCPSpec20241105)},
}

// TestLegacy_Notification_NoReply: a request with no id member is a
// JSON-RPC notification, which must never be answered. The legacy
// path acknowledges it with HTTP 202 and an empty body, and does not
// process it, matching the modern handler's V2 answer.
func TestLegacy_Notification_NoReply(t *testing.T) {
	for name, opts := range legacyMounts {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			build := func(root *cobra.Command) *Bridge {
				return New(root, WithRunner(&fakeRunner{
					run: func(context.Context, Invocation) (Result, error) {
						calls.Add(1)
						return Result{Stdout: "ok"}, nil
					},
				}))
			}
			srv := legacyLockServer(t, build, opts...)
			for _, body := range []string{
				`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
				`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`, //nolint:misspell // MCP method name
				`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"ping"}}`,
			} {
				status, _, raw := rawPOST(t, srv, "/mcp", nil, []byte(body))
				if status != http.StatusAccepted {
					t.Errorf("%s: status=%d want=202", body, status)
				}
				if len(raw) != 0 {
					t.Errorf("%s: body=%q want empty", body, raw)
				}
			}
			if n := calls.Load(); n != 0 {
				t.Errorf("runner invoked %d times for notifications, want 0", n)
			}
		})
	}
}

// TestLegacy_NullID_IsARequest: "id": null is present, so the message
// is a request, not a notification, and is still answered.
func TestLegacy_NullID_IsARequest(t *testing.T) {
	srv := legacyLockServer(t, nil)
	status, _, raw := rawPOST(t, srv, "/mcp", nil,
		[]byte(`{"jsonrpc":"2.0","id":null,"method":"ping"}`))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	assertByteExact(t, "ping null id", raw, []byte(`{"jsonrpc":"2.0","id":null,"result":{}}`+"\n"))
}

// TestLegacy_Ping_EmptyResult: 2024-11-05 requires the receiver of a
// ping to answer promptly with an empty result.
func TestLegacy_Ping_EmptyResult(t *testing.T) {
	for name, opts := range legacyMounts {
		t.Run(name, func(t *testing.T) {
			srv := legacyLockServer(t, nil, opts...)
			for _, tc := range []struct{ body, want string }{
				{`{"jsonrpc":"2.0","id":7,"method":"ping"}`, `{"jsonrpc":"2.0","id":7,"result":{}}`},
				{`{"jsonrpc":"2.0","id":"p","method":"ping","params":{}}`, `{"jsonrpc":"2.0","id":"p","result":{}}`},
			} {
				status, _, raw := rawPOST(t, srv, "/mcp", nil, []byte(tc.body))
				if status != http.StatusOK {
					t.Errorf("%s: status=%d want=200", tc.body, status)
				}
				assertByteExact(t, "ping "+tc.body, raw, []byte(tc.want+"\n"))
			}
		})
	}
}
