package mcpsdk

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// modernMeta is a complete 2026-07-28 request _meta.
const modernMeta = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{"name":"revision-probe","version":"0"}}`

// TestPerRequestRevisionRouting pins the classification rules: the
// routing precedence of docs/adopters/guides/expose-cli-over-mcp.md,
// including its edge-case table.
func TestPerRequestRevisionRouting(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		body    string
		modern  bool
	}{
		{"unparseable body", nil, `{not json`, false},
		{"batch", map[string]string{"Mcp-Method": "tools/list"}, `[{"jsonrpc":"2.0","id":1,"method":"tools/list"}]`, false},
		{"initialize", nil, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`, false},
		{"initialize with every marker", map[string]string{"Mcp-Method": "initialize", "Mcp-Name": "x"},
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{` + modernMeta + `}}`, false},
		{"tools/list, no marker", nil, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, false},
		{"protocol-version header alone", map[string]string{"MCP-Protocol-Version": "2026-07-28"},
			`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, false},
		{"_meta without the reserved key", nil,
			`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"progressToken":"p"}}}`, false},
		{"_meta not an object", nil,
			`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":7}}`, false},
		{"unknown method, no marker", nil, `{"jsonrpc":"2.0","id":1,"method":"nope"}`, false},
		{"bare server/discover", nil, `{"jsonrpc":"2.0","id":1,"method":"server/discover"}`, true},
		{"Mcp-Method header only", map[string]string{"Mcp-Method": "tools/call"},
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}`, true},
		{"Mcp-Name header only", map[string]string{"Mcp-Name": "ping"},
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping"}}`, true},
		{"reserved _meta key only", nil,
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping",` + modernMeta + `}}`, true},
		{"notification with markers", map[string]string{"Mcp-Method": "notifications/progress"},
			`{"jsonrpc":"2.0","method":"notifications/progress"}`, true},
	}
	for _, tc := range cases {
		h := http.Header{}
		for k, v := range tc.headers {
			h.Set(k, v)
		}
		if got := perRequestRevision(h, []byte(tc.body)); got != tc.modern {
			t.Errorf("%s: per-request = %v, want %v", tc.name, got, tc.modern)
		}
	}
}

// TestPerRequestRevisionRoutingMatchesWireFixtures classifies every
// request of the cross-language wire fixtures the way the hand-rolled
// surface that generated them routed it: by its era, except that an
// initialize is always a session request (the fixture labels its D2
// case by its markers, while its response is the legacy handshake).
func TestPerRequestRevisionRoutingMatchesWireFixtures(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "sdk", "tests", "cross-lang", "fixtures", "mcp-wire.json"))
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	type step struct {
		Name    string            `json:"name"`
		Era     string            `json:"era"`
		Headers map[string]string `json:"headers"`
		Request string            `json:"request"`
	}
	var fx struct {
		Cases     []step `json:"cases"`
		Sequences []struct {
			Steps []step `json:"steps"`
		} `json:"sequences"`
		MRTR struct {
			Round1Headers map[string]string `json:"round1_headers"`
			Round1Request string            `json:"round1_request"`
		} `json:"mrtr"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("decode fixtures: %v", err)
	}
	steps := append([]step(nil), fx.Cases...)
	for _, s := range fx.Sequences {
		steps = append(steps, s.Steps...)
	}
	steps = append(steps, step{Name: "mrtr/round1", Era: "modern", Headers: fx.MRTR.Round1Headers, Request: fx.MRTR.Round1Request})
	if len(steps) < 10 {
		t.Fatalf("only %d fixture requests decoded", len(steps))
	}
	for _, s := range steps {
		h := http.Header{}
		for k, v := range s.Headers {
			h.Set(k, v)
		}
		want := s.Era == "modern" && !strings.Contains(s.Request, `"method":"initialize"`)
		if got := perRequestRevision(h, []byte(s.Request)); got != want {
			t.Errorf("%s: per-request = %v, want %v", s.Name, got, want)
		}
	}
}

// postJSON sends one JSON-RPC body with headers and returns the
// response and its (SSE-unwrapped) JSON payload.
func postJSON(t *testing.T, url string, hdr map[string]string, body string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	payload := string(b)
	for _, line := range strings.Split(payload, "\n") {
		if strings.HasPrefix(line, "data:") {
			payload = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			break
		}
	}
	return resp, strings.TrimSpace(payload)
}

// modernHeaders frames a 2026-07-28 request.
func modernHeaders(method, name string) map[string]string {
	h := map[string]string{"MCP-Protocol-Version": "2026-07-28", "Mcp-Method": method}
	if name != "" {
		h["Mcp-Name"] = name
	}
	return h
}

// TestHandlerServesBothRevisions: the default handler keeps sessions
// for the initialize handshake and answers initialize-less 2026-07-28
// requests on the same endpoint, with neither disturbing the other.
func TestHandlerServesBothRevisions(t *testing.T) {
	srv, _ := newHarness(t, defaultBridge)
	url := srv.URL + "/mcp"

	resp, payload := postJSON(t, url, nil, initializeBody("2024-11-05"))
	if resp.StatusCode != http.StatusOK || !strings.Contains(payload, `"protocolVersion":"2024-11-05"`) {
		t.Fatalf("initialize: status %d, payload %s", resp.StatusCode, payload)
	}
	sid := resp.Header.Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatal("initialize issued no Mcp-Session-Id: the session handler did not serve it")
	}
	legacy := map[string]string{"Mcp-Session-Id": sid, "MCP-Protocol-Version": "2024-11-05"}
	if resp, _ := postJSON(t, url, legacy, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("initialized: status %d", resp.StatusCode)
	}

	resp, payload = postJSON(t, url, modernHeaders("server/discover", ""),
		`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{`+modernMeta+`}}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(payload, `"2026-07-28"`) {
		t.Fatalf("server/discover: status %d, payload %s", resp.StatusCode, payload)
	}
	resp, payload = postJSON(t, url, modernHeaders("tools/call", "ping"),
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ping","arguments":{},`+modernMeta+`}}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(payload, "pong") {
		t.Fatalf("modern tools/call: status %d, payload %s", resp.StatusCode, payload)
	}
	if resp.Header.Get("Mcp-Session-Id") != "" {
		t.Error("a 2026-07-28 response carries Mcp-Session-Id")
	}

	resp, payload = postJSON(t, url, legacy, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ping","arguments":{}}}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(payload, "pong") {
		t.Fatalf("session tools/call after modern traffic: status %d, payload %s", resp.StatusCode, payload)
	}
}

// TestHandlerStatelessOptionServesOnlyPerRequest: WithStateless keeps
// its meaning — one stateless handler, no sessions for anyone.
func TestHandlerStatelessOptionServesOnlyPerRequest(t *testing.T) {
	srv, _ := newHarness(t, defaultBridge, WithStateless())
	resp, _ := postJSON(t, srv.URL+"/mcp", nil, initializeBody("2024-11-05"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize: status %d", resp.StatusCode)
	}
	if resp.Header.Get("Mcp-Session-Id") != "" {
		t.Error("a stateless handler issued Mcp-Session-Id")
	}
}

// sessionOnlyTransport makes the SDK client behave like one that
// predates 2026-07-28: it answers the client's server/discover probe
// itself with method-not-found, so the client falls back to the
// initialize handshake and a session.
type sessionOnlyTransport struct{}

func (sessionOnlyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost || req.Header.Get("Mcp-Method") != "server/discover" {
		return http.DefaultTransport.RoundTrip(req)
	}
	var msg struct {
		ID json.RawMessage `json:"id"`
	}
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		_ = req.Body.Close()
		_ = json.Unmarshal(b, &msg)
	}
	body := `{"jsonrpc":"2.0","id":` + string(msg.ID) + `,"error":{"code":-32601,"message":"method not found"}}`
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

// TestSessionClientElicitationOnTheDefaultHandler: a client on a
// revision before 2026-07-28 keeps its session, and the confirmation
// question reaches it as elicitation/create on that session.
func TestSessionClientElicitationOnTheDefaultHandler(t *testing.T) {
	srv, _, _ := newSurfaceHarness(t, newTestTree(), nil, WithConfirmationElicitation(nil))
	asked := 0
	client := mcp.NewClient(&mcp.Implementation{Name: "session-client", Version: "0"}, &mcp.ClientOptions{
		ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			asked++
			return &mcp.ElicitResult{Action: "accept"}, nil
		},
	})
	sess, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: sessionOnlyTransport{}},
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	if v := sess.InitializeResult().ProtocolVersion; v >= "2026-07-28" {
		t.Fatalf("negotiated %s, want a session revision", v)
	}
	if sess.ID() == "" {
		t.Fatal("no Mcp-Session-Id: the session handler did not serve the handshake")
	}

	text, isErr := callText(t, sess, "deploy")
	if isErr || !strings.Contains(text, "deployed") || asked != 1 {
		t.Fatalf("isError=%t text=%q asked=%d, want one question on the session, then the call", isErr, text, asked)
	}
}

// TestHandlerRoutingKeepsTheSDKBodyLimit: classifying reads the body
// first, and the SDK still sees all of it and applies its own limit.
func TestHandlerRoutingKeepsTheSDKBodyLimit(t *testing.T) {
	srv, _ := newHarness(t, defaultBridge)
	pad := strings.Repeat("x", int(mcp.DefaultMaxRequestBodyBytes))
	resp, _ := postJSON(t, srv.URL+"/mcp", modernHeaders("tools/call", "ping"),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping","pad":"`+pad+`"}}`)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: status %d, want 413", resp.StatusCode)
	}
}
