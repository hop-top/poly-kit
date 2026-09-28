package mcpsdk

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"hop.top/kit/go/transport/api"
)

// postInitialize sends a raw initialize request carrying origin, the
// way a browser page would, and returns the response.
func postInitialize(t *testing.T, url, origin string) *http.Response {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestOriginRefused(t *testing.T) {
	// The MCP streamable HTTP transport requires servers to validate
	// Origin: a page on another site must not drive the tool set.
	srv, _ := newHarness(t, defaultBridge, WithOriginAllowlist())

	resp := postInitialize(t, srv.URL+"/mcp", "https://attacker.example")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	var e struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   struct {
			Code    int               `json:"code"`
			Message string            `json:"message"`
			Data    map[string]string `json:"data"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		t.Fatalf("decode refusal: %v", err)
	}
	if e.JSONRPC != "2.0" || string(e.ID) != "null" || e.Error.Code != -32600 {
		t.Errorf("envelope = %+v, want JSON-RPC 2.0 error -32600 with id null", e)
	}
	if !strings.HasPrefix(e.Error.Message, api.CodeOriginRejected) || e.Error.Data["code"] != api.CodeOriginRejected {
		t.Errorf("error = %+v, want code %q", e.Error, api.CodeOriginRejected)
	}
}

func TestOriginAbsentOrSameOriginAllowed(t *testing.T) {
	srv, _ := newHarness(t, defaultBridge, WithOriginAllowlist())

	if resp := postInitialize(t, srv.URL+"/mcp", ""); resp.StatusCode != http.StatusOK {
		t.Errorf("no Origin: status = %d, want 200", resp.StatusCode)
	}
	if resp := postInitialize(t, srv.URL+"/mcp", srv.URL); resp.StatusCode != http.StatusOK {
		t.Errorf("same origin: status = %d, want 200", resp.StatusCode)
	}

	// The SDK client end to end, with a same-origin header on every
	// request (initialize, notifications, the GET stream, DELETE).
	sess := connect(t, srv.URL+"/mcp", map[string]string{"Origin": srv.URL})
	if _, err := sess.ListTools(t.Context(), nil); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
}

func TestOriginAllowlist(t *testing.T) {
	srv, _ := newHarness(t, defaultBridge, WithOriginAllowlist("https://app.example.com"))

	sess := connect(t, srv.URL+"/mcp", map[string]string{
		"Origin":         "https://app.example.com",
		"Sec-Fetch-Site": "cross-site",
	})
	if _, err := sess.ListTools(t.Context(), nil); err != nil {
		t.Fatalf("ListTools from allowlisted origin: %v", err)
	}
	if resp := postInitialize(t, srv.URL+"/mcp", "https://other.example.com"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("unlisted origin: status = %d, want 403", resp.StatusCode)
	}
}

func TestOriginUncheckedWithoutOption(t *testing.T) {
	// Without the option the listener in front owns the check (the
	// api service's Host and Origin slot); the surface adds no
	// second one.
	srv, _ := newHarness(t, defaultBridge)
	if resp := postInitialize(t, srv.URL+"/mcp", "https://attacker.example"); resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 with no surface-level check", resp.StatusCode)
	}
}

func TestOriginAllowlistRejectsMalformedEntry(t *testing.T) {
	if _, err := New(defaultBridge(newTestTree()), WithOriginAllowlist("app.example.com")); err == nil {
		t.Error("New accepted an allowlist entry that is not an origin")
	}
}

// postInitializeAs sends the initialize request with the Host header
// set to host, the way a DNS-rebinding page's request arrives.
func postInitializeAs(t *testing.T, url, host string) int {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// The SDK's DNS-rebinding check is in force on every handler, stateful
// and stateless, until a listener that checks Host itself turns it off.
func TestLocalhostProtection(t *testing.T) {
	for _, stateless := range []bool{false, true} {
		var base []Option
		if stateless {
			base = append(base, WithStateless())
		}
		srv, _ := newHarness(t, defaultBridge, base...)
		if got := postInitializeAs(t, srv.URL+"/mcp", "tool.example"); got != http.StatusForbidden {
			t.Errorf("stateless=%v: status = %d, want the SDK's 403", stateless, got)
		}
		srv, _ = newHarness(t, defaultBridge, append(base, WithoutLocalhostProtection())...)
		if got := postInitializeAs(t, srv.URL+"/mcp", "tool.example"); got != http.StatusOK {
			t.Errorf("stateless=%v, protection off: status = %d, want 200", stateless, got)
		}
	}
}
