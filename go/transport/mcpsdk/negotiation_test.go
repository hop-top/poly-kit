package mcpsdk

// These tests speak raw JSON-RPC over HTTP on purpose: the SDK
// client always requests the newest protocol version, so exercising
// the server's negotiation behavior for older versions requires
// impersonating clients at each version. The server side under test
// remains 100% SDK.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hop.top/kit/go/transport/cmdsurface"
)

// rawPost sends one JSON-RPC request body and returns the response
// plus its (SSE-unwrapped) JSON payload.
func rawPost(t *testing.T, url, body string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	payload := string(raw)
	for _, line := range strings.Split(payload, "\n") {
		if strings.HasPrefix(line, "data:") {
			payload = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			break
		}
	}
	return resp, strings.TrimSpace(payload)
}

func initializeBody(version string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":%q,"capabilities":{},"clientInfo":{"name":"version-probe","version":"0"}}}`, version)
}

// TestVersionNegotiation pins the SDK server's negotiation behavior
// for the legacy initialize handshake: every version from 2024-11-05
// through 2025-11-25 is echoed back verbatim; 2026-07-28 (which
// replaces initialize with per-request negotiation) and unknown
// versions fall back to 2025-11-25.
func TestVersionNegotiation(t *testing.T) {
	srv, _ := newHarness(t, defaultBridge)
	url := srv.URL + "/mcp"

	cases := []struct {
		client string
		want   string
	}{
		{"2024-11-05", "2024-11-05"},
		{"2025-03-26", "2025-03-26"},
		{"2025-06-18", "2025-06-18"},
		{"2025-11-25", "2025-11-25"},
		{"2026-07-28", "2025-11-25"},
		{"1999-01-01", "2025-11-25"},
	}
	for _, tc := range cases {
		resp, payload := rawPost(t, url, initializeBody(tc.client))
		if resp.StatusCode != http.StatusOK {
			t.Errorf("client=%s: status = %d, want 200", tc.client, resp.StatusCode)
			continue
		}
		var env struct {
			Result struct {
				ProtocolVersion string `json:"protocolVersion"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(payload), &env); err != nil {
			t.Errorf("client=%s: unmarshal %q: %v", tc.client, payload, err)
			continue
		}
		if env.Result.ProtocolVersion != tc.want {
			t.Errorf("client=%s: negotiated = %s, want %s",
				tc.client, env.Result.ProtocolVersion, tc.want)
		}
	}
}

// TestNewProtocolPerRequest verifies the 2026-07-28 initialize-less
// path: a single request carrying version + capabilities in _meta and
// the Mcp-* framing headers is served at the new protocol version.
func TestNewProtocolPerRequest(t *testing.T) {
	b := cmdsurface.New(newTestTree())
	h, err := Handler(b, WithStateless(), WithJSONResponse())
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping","arguments":{},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{"name":"version-probe","version":"0"}}}}`
	req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", "ping")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "pong") {
		t.Errorf("body = %s, want to contain pong", raw)
	}
}

// TestStatelessMode verifies SEP-2567 stateless behavior: requests
// need no session, and GET is rejected with 405.
func TestStatelessMode(t *testing.T) {
	b := cmdsurface.New(newTestTree())
	h, err := Handler(b, WithStateless(), WithJSONResponse())
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	resp, payload := rawPost(t, srv.URL, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tools/list status = %d, body = %s", resp.StatusCode, payload)
	}
	if !strings.Contains(payload, `"widget.add"`) {
		t.Errorf("tools/list payload missing widget.add: %s", payload)
	}
	if resp.Header.Get("Mcp-Session-Id") != "" {
		t.Error("stateless response carries Mcp-Session-Id")
	}

	getResp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("stateless GET status = %d, want 405", getResp.StatusCode)
	}
}

// TestSDKNativeTasksCanary is the reconcile-with-upstream signal.
// Kit now implements SEP-2663 itself (the tasks extension module plus
// the WithTasks binding) precisely because go-sdk v1.7.0 ships no
// tasks support and rejects every tasks/* method at the transport
// layer with exactly HTTP 400 and a `... "tasks/*" unsupported` body.
// This canary pins that rejection against a BARE SDK server — no kit
// surface, no tasks interceptor in front — so that ANY change in how
// a future SDK answers tasks/* (native SEP-2663 support, or the
// per-tool opt-in shape of go-sdk PR #755) reddens it and forces the
// kit-side extension to be reconciled with upstream: the moment the
// SDK routes these methods itself, the standing rule against
// duplicating SDK behavior applies again. A message tweak tripping it
// is an acceptable false positive; staying silent through an SDK
// upgrade that ships tasks is not.
func TestSDKNativeTasksCanary(t *testing.T) {
	bare := mcp.NewServer(&mcp.Implementation{Name: "canary", Version: "0"}, nil)
	h := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return bare },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	for _, tc := range []struct {
		method string
		body   string
	}{
		{"tasks/get", `{"jsonrpc":"2.0","id":1,"method":"tasks/get","params":{"taskId":"t1"}}`},
		{"tasks/update", `{"jsonrpc":"2.0","id":2,"method":"tasks/update","params":{"taskId":"t1","inputResponses":{}}}`},
		{"tasks/cancel", `{"jsonrpc":"2.0","id":3,"method":"tasks/cancel","params":{"taskId":"t1"}}`},
		{"tasks/list", `{"jsonrpc":"2.0","id":4,"method":"tasks/list"}`},
		{"tasks/result", `{"jsonrpc":"2.0","id":5,"method":"tasks/result","params":{"taskId":"t1"}}`},
	} {
		resp, payload := rawPost(t, srv.URL, tc.body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 — the SDK now answers tasks/* itself; reconcile the kit-side extension with upstream (payload: %s)",
				tc.method, resp.StatusCode, payload)
		}
		if !strings.Contains(payload, "unsupported") {
			t.Errorf("%s: payload = %q, want to contain %q — the SDK now answers tasks/* itself; reconcile the kit-side extension with upstream",
				tc.method, payload, "unsupported")
		}
	}
}
