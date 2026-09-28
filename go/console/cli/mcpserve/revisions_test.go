package mcpserve_test

// Both protocol revisions against one running mcp service endpoint.
// These speak raw JSON-RPC on purpose: the SDK client picks its own
// revision, and the point here is what each revision's client sends.

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/mcpserve"
)

// modernMeta returns a complete 2026-07-28 request _meta with caps as
// the client capabilities.
func modernMeta(caps string) string {
	return `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":` + caps + `,"io.modelcontextprotocol/clientInfo":{"name":"modern-probe","version":"0"}}`
}

// wireReply is one response: status, headers, and the JSON-RPC
// payload unwrapped from SSE when the server streamed it.
type wireReply struct {
	status  int
	header  http.Header
	payload string
}

func (r wireReply) decode(t *testing.T, v any) {
	t.Helper()
	require.NoError(t, json.Unmarshal([]byte(r.payload), v), r.payload)
}

func post(t *testing.T, endpoint string, hdr map[string]string, body string) wireReply {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	payload := string(raw)
	for _, line := range strings.Split(payload, "\n") {
		if strings.HasPrefix(line, "data:") {
			payload = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			break
		}
	}
	return wireReply{status: resp.StatusCode, header: resp.Header, payload: strings.TrimSpace(payload)}
}

// toolReply is the part of a tools/call result these tests read.
type toolReply struct {
	Result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError         bool                       `json:"isError"`
		ResultType      string                     `json:"resultType"`
		InputRequests   map[string]json.RawMessage `json:"inputRequests"`
		RequestState    string                     `json:"requestState"`
		ProtocolVersion string                     `json:"protocolVersion"`
		Tools           []struct {
			Name string `json:"name"`
		} `json:"tools"`
		SupportedVersions []string `json:"supportedVersions"`
	} `json:"result"`
}

func (r toolReply) text() string {
	var b strings.Builder
	for _, c := range r.Result.Content {
		b.WriteString(c.Text)
	}
	return b.String()
}

func (r toolReply) names() []string {
	var out []string
	for _, tool := range r.Result.Tools {
		out = append(out, tool.Name)
	}
	return out
}

// legacyClient speaks 2024-11-05: initialize, then every request on
// the session the server issued.
type legacyClient struct {
	t        *testing.T
	endpoint string
	hdr      map[string]string
	id       int
}

func newLegacyClient(t *testing.T, endpoint string, hdr map[string]string) *legacyClient {
	t.Helper()
	c := &legacyClient{t: t, endpoint: endpoint, hdr: map[string]string{}}
	for k, v := range hdr {
		c.hdr[k] = v
	}
	r := post(t, endpoint, c.hdr, `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"legacy-probe","version":"0"}}}`)
	require.Equal(t, http.StatusOK, r.status, r.payload)
	var init toolReply
	r.decode(t, &init)
	require.Equal(t, "2024-11-05", init.Result.ProtocolVersion)
	sid := r.header.Get("Mcp-Session-Id")
	require.NotEmpty(t, sid, "a 2024-11-05 client gets a session")
	c.hdr["Mcp-Session-Id"] = sid
	c.hdr["MCP-Protocol-Version"] = "2024-11-05"
	r = post(t, endpoint, c.hdr, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	require.Equal(t, http.StatusAccepted, r.status, r.payload)
	return c
}

func (c *legacyClient) call(method, params string, extra map[string]string) (wireReply, toolReply) {
	c.t.Helper()
	c.id++
	hdr := map[string]string{}
	for k, v := range c.hdr {
		hdr[k] = v
	}
	for k, v := range extra {
		hdr[k] = v
	}
	body := `{"jsonrpc":"2.0","id":` + itoa(c.id) + `,"method":"` + method + `","params":` + params + `}`
	r := post(c.t, c.endpoint, hdr, body)
	var tr toolReply
	if r.status == http.StatusOK {
		r.decode(c.t, &tr)
	}
	return r, tr
}

// modernCall sends one initialize-less 2026-07-28 request.
func modernCall(t *testing.T, endpoint, method, name, params string, extra map[string]string) (wireReply, toolReply) {
	t.Helper()
	hdr := map[string]string{"MCP-Protocol-Version": "2026-07-28", "Mcp-Method": method}
	if name != "" {
		hdr["Mcp-Name"] = name
	}
	for k, v := range extra {
		hdr[k] = v
	}
	r := post(t, endpoint, hdr, `{"jsonrpc":"2.0","id":1,"method":"`+method+`","params":`+params+`}`)
	assert.Empty(t, r.header.Get("Mcp-Session-Id"), "2026-07-28 is served without a session")
	var tr toolReply
	if r.status == http.StatusOK {
		r.decode(t, &tr)
	}
	return r, tr
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestMCPServiceHTTPServesBothRevisionsOnOneEndpoint(t *testing.T) {
	_, endpoint := startMCP(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"})

	t.Run("2024-11-05", func(t *testing.T) {
		c := newLegacyClient(t, endpoint, nil)

		_, list := c.call("tools/list", `{}`, nil)
		assert.Subset(t, list.names(), []string{"ping", "secret", "deploy"})
		assert.NotContains(t, list.names(), "nuke")

		_, res := c.call("tools/call", `{"name":"ping","arguments":{}}`, nil)
		assert.False(t, res.Result.IsError)
		assert.Equal(t, "pong", res.text())

		// No elicitation declared, no header: refused, naming both remedies.
		_, res = c.call("tools/call", `{"name":"deploy","arguments":{}}`, nil)
		assert.True(t, res.Result.IsError)
		assert.Contains(t, res.text(), "confirmation required")
		assert.Contains(t, res.text(), "X-Confirm-Token")
		_, res = c.call("tools/call", `{"name":"deploy","arguments":{}}`, map[string]string{"X-Confirm-Token": "yes"})
		assert.False(t, res.Result.IsError, res.text())
		assert.Equal(t, "deployed", res.text())

		// A bare Authorization header is presence, not authentication.
		_, res = c.call("tools/call", `{"name":"secret","arguments":{}}`, map[string]string{"Authorization": "Bearer made-up"})
		assert.True(t, res.Result.IsError)
		assert.Equal(t, "authentication required", res.text())
	})

	t.Run("2026-07-28", func(t *testing.T) {
		r, disc := modernCall(t, endpoint, "server/discover", "", `{`+modernMeta(`{}`)+`}`, nil)
		require.Equal(t, http.StatusOK, r.status, r.payload)
		assert.Contains(t, disc.Result.SupportedVersions, "2026-07-28")
		assert.Contains(t, disc.Result.SupportedVersions, "2024-11-05")

		r, list := modernCall(t, endpoint, "tools/list", "", `{`+modernMeta(`{}`)+`}`, nil)
		require.Equal(t, http.StatusOK, r.status, r.payload)
		assert.Subset(t, list.names(), []string{"ping", "secret", "deploy"})
		assert.NotContains(t, list.names(), "nuke")

		r, res := modernCall(t, endpoint, "tools/call", "ping", `{"name":"ping","arguments":{},`+modernMeta(`{}`)+`}`, nil)
		require.Equal(t, http.StatusOK, r.status, r.payload)
		assert.False(t, res.Result.IsError)
		assert.Equal(t, "pong", res.text())

		// Elicitation declared: the question comes back as an
		// input_required result, and the retry carrying the accepted
		// answer and the echoed state runs the call once.
		elicit := modernMeta(`{"elicitation":{"form":{}}}`)
		r, res = modernCall(t, endpoint, "tools/call", "deploy", `{"name":"deploy","arguments":{},`+elicit+`}`, nil)
		require.Equal(t, http.StatusOK, r.status, r.payload)
		require.Equal(t, "input_required", res.Result.ResultType, r.payload)
		require.Len(t, res.Result.InputRequests, 1, r.payload)
		require.NotEmpty(t, res.Result.RequestState)
		assert.NotContains(t, r.payload, "deployed", "nothing runs before the answer")
		var key string
		for k := range res.Result.InputRequests {
			key = k
		}
		state, _ := json.Marshal(res.Result.RequestState)
		retry := `{"name":"deploy","arguments":{},"requestState":` + string(state) +
			`,"inputResponses":{"` + key + `":{"action":"accept"}},` + elicit + `}`
		r, res = modernCall(t, endpoint, "tools/call", "deploy", retry, nil)
		require.Equal(t, http.StatusOK, r.status, r.payload)
		assert.False(t, res.Result.IsError, r.payload)
		assert.Equal(t, "deployed", res.text())

		declined := strings.Replace(retry, `"accept"`, `"decline"`, 1)
		_, res = modernCall(t, endpoint, "tools/call", "deploy", declined, nil)
		assert.True(t, res.Result.IsError)
		assert.Equal(t, "confirmation declined", res.text())

		// Neither elicitation nor the header: refused.
		_, res = modernCall(t, endpoint, "tools/call", "deploy", `{"name":"deploy","arguments":{},`+modernMeta(`{}`)+`}`, nil)
		assert.True(t, res.Result.IsError)
		assert.Contains(t, res.text(), "confirmation required")
		_, res = modernCall(t, endpoint, "tools/call", "deploy", `{"name":"deploy","arguments":{},`+modernMeta(`{}`)+`}`,
			map[string]string{"X-Confirm-Token": "yes"})
		assert.False(t, res.Result.IsError, res.text())
		assert.Equal(t, "deployed", res.text())

		_, res = modernCall(t, endpoint, "tools/call", "secret", `{"name":"secret","arguments":{},`+modernMeta(`{}`)+`}`,
			map[string]string{"Authorization": "Bearer made-up"})
		assert.True(t, res.Result.IsError)
		assert.Equal(t, "authentication required", res.text())
	})

	t.Run("sessions survive per-request traffic", func(t *testing.T) {
		c := newLegacyClient(t, endpoint, nil)
		modernCall(t, endpoint, "tools/call", "ping", `{"name":"ping","arguments":{},`+modernMeta(`{}`)+`}`, nil)
		_, res := c.call("tools/call", `{"name":"ping","arguments":{}}`, nil)
		assert.Equal(t, "pong", res.text())
	})
}

func TestMCPServiceHTTPVerifiedAuthOnBothRevisions(t *testing.T) {
	_, endpoint := startMCP(t, mcpserve.Config{Auth: bearerAuth}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"})
	good := map[string]string{"Authorization": "Bearer good"}

	t.Run("2024-11-05", func(t *testing.T) {
		r := post(t, endpoint, nil, `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"legacy-probe","version":"0"}}}`)
		assert.Equal(t, http.StatusUnauthorized, r.status, "refused before the SDK sees it")

		c := newLegacyClient(t, endpoint, good)
		_, res := c.call("tools/call", `{"name":"secret","arguments":{}}`, nil)
		assert.False(t, res.Result.IsError, res.text())
		assert.Equal(t, "unlocked", res.text())
	})

	t.Run("2026-07-28", func(t *testing.T) {
		params := `{"name":"secret","arguments":{},` + modernMeta(`{}`) + `}`
		r, _ := modernCall(t, endpoint, "tools/call", "secret", params, nil)
		assert.Equal(t, http.StatusUnauthorized, r.status, "refused before the SDK sees it")

		r, res := modernCall(t, endpoint, "tools/call", "secret", params, good)
		require.Equal(t, http.StatusOK, r.status, r.payload)
		assert.False(t, res.Result.IsError, res.text())
		assert.Equal(t, "unlocked", res.text())
	})
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

// TestMCPServiceHTTPSessionElicitation: a session client is asked
// with elicitation/create on its session, as before 2026-07-28 was
// served.
func TestMCPServiceHTTPSessionElicitation(t *testing.T) {
	_, endpoint := startMCP(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"})

	for _, tc := range []struct {
		action string
		want   string
		isErr  bool
	}{
		{"accept", "deployed", false},
		{"decline", "confirmation declined", true},
	} {
		t.Run(tc.action, func(t *testing.T) {
			var asked atomic.Int32
			client := mcp.NewClient(&mcp.Implementation{Name: "session-client", Version: "0"}, &mcp.ClientOptions{
				ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
					asked.Add(1)
					return &mcp.ElicitResult{Action: tc.action}, nil
				},
			})
			sess, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
				Endpoint: endpoint, HTTPClient: &http.Client{Transport: sessionOnlyTransport{}},
			}, nil)
			require.NoError(t, err)
			t.Cleanup(func() { _ = sess.Close() })
			require.NotEmpty(t, sess.ID(), "a session client gets a session")
			require.Less(t, sess.InitializeResult().ProtocolVersion, "2026-07-28")

			text, isErr := callTool(t, sess, "deploy", nil)
			assert.Equal(t, tc.isErr, isErr, text)
			assert.Equal(t, tc.want, text)
			assert.Equal(t, int32(1), asked.Load(), "one question, on the session")
		})
	}
}

// TestMCPServiceHTTPStopsPromptlyWithAnUnusedConnection: a client
// connection that never sent a request does not hold the stop.
func TestMCPServiceHTTPStopsPromptlyWithAnUnusedConnection(t *testing.T) {
	run, endpoint := startMCP(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"})
	u, err := url.Parse(endpoint)
	require.NoError(t, err)
	idle, err := net.Dial("tcp", u.Host)
	require.NoError(t, err)
	defer idle.Close()
	// A request on a connection dialed later: once it is answered, the
	// server has accepted the idle one before it.
	r, _ := modernCall(t, endpoint, "tools/list", "", `{`+modernMeta(`{}`)+`}`, nil)
	require.Equal(t, http.StatusOK, r.status)

	start := time.Now()
	run.stop()
	select {
	case err := <-run.errCh:
		run.errCh <- err
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return after cancellation")
	}
	assert.Less(t, time.Since(start), 2*time.Second, "stop waited on a connection that carried no request")
}

// TestMCPServiceHTTPKeepsAliveAcrossRequests: releasing a stalled
// read on stop must not end reads while the service runs — every
// request on one kept-alive connection is answered.
func TestMCPServiceHTTPKeepsAliveAcrossRequests(t *testing.T) {
	_, endpoint := startMCP(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"})
	u, err := url.Parse(endpoint)
	require.NoError(t, err)
	conn, err := net.Dial("tcp", u.Host)
	require.NoError(t, err)
	defer conn.Close()
	br := bufio.NewReader(conn)
	for i := range 200 {
		_, err = io.WriteString(conn, "GET /elsewhere HTTP/1.1\r\nHost: "+u.Host+"\r\n\r\n")
		require.NoError(t, err, "request %d", i)
		resp, err := http.ReadResponse(br, nil)
		require.NoError(t, err, "request %d", i)
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		require.Equal(t, http.StatusNotFound, resp.StatusCode, "request %d", i)
	}
}

// TestMCPServiceHTTPStopsPromptlyWithAStalledRequest: a client that
// sends part of a request and then stalls does not hold the stop —
// not for the read-header timeout when it stalls mid-header, whether
// that opens the connection or follows a request it already carried,
// and not for the whole stop budget when it stalls mid-body, whether
// the SDK is reading the body or the Host check refused the request
// before anything read it.
func TestMCPServiceHTTPStopsPromptlyWithAStalledRequest(t *testing.T) {
	const midHeader = "POST /mcp HTTP/1.1\r\nHost: {host}\r\nContent-Type: appl"
	const midBody = "POST /mcp HTTP/1.1\r\nHost: {host}\r\n" +
		"Content-Type: application/json\r\nAccept: application/json, text/event-stream\r\n" +
		"Content-Length: 200\r\n\r\n{\"jsonrpc\":"
	for _, tc := range []struct {
		name    string
		first   string // a complete request sent before the stall, if any
		partial string
		host    string // the Host sent; the listener's own when empty
	}{
		{name: "mid-header, first request", partial: midHeader},
		{name: "mid-header, after a request", first: "GET /elsewhere HTTP/1.1\r\nHost: {host}\r\n\r\n", partial: midHeader},
		{name: "mid-body", partial: midBody},
		{name: "mid-body, refused unread", partial: midBody, host: "evil.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run, endpoint := startMCP(t, mcpserve.Config{}, []string{"mcp", "--mcp-addr", "127.0.0.1:0"})
			u, err := url.Parse(endpoint)
			require.NoError(t, err)
			host := tc.host
			if host == "" {
				host = u.Host
			}
			tc.first = strings.ReplaceAll(tc.first, "{host}", host)
			tc.partial = strings.ReplaceAll(tc.partial, "{host}", host)
			conn, err := net.Dial("tcp", u.Host)
			require.NoError(t, err)
			defer conn.Close()
			if tc.first != "" {
				_, err = io.WriteString(conn, tc.first)
				require.NoError(t, err)
				resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
				require.NoError(t, err)
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			_, err = io.WriteString(conn, tc.partial)
			require.NoError(t, err)
			// A request on another connection: once it is answered, the
			// server has accepted the stalled one and is reading from it.
			r, _ := modernCall(t, endpoint, "tools/list", "", `{`+modernMeta(`{}`)+`}`, nil)
			require.Equal(t, http.StatusOK, r.status)

			start := time.Now()
			run.stop()
			select {
			case err := <-run.errCh:
				run.errCh <- err
			case <-time.After(10 * time.Second):
				t.Fatal("serve did not return after cancellation")
			}
			assert.Less(t, time.Since(start), 2*time.Second, "stop waited on a stalled request")
		})
	}
}
