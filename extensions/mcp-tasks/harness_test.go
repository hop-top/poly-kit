// Copyright 2026 The Model Context Protocol Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package tasks_test

// Test harness: a real *mcp.Server behind the SDK's own streamable
// HTTP handler (stateless, JSON responses), with the extension
// attached so its methods are dispatched by that handler like any
// other. Requests are raw JSON-RPC at protocol 2026-07-28 with
// per-request _meta negotiation — deliberately: the SDK client
// unmarshals tool results into CallToolResult, which drops the
// CreateTaskResult fields this extension adds, so wire-shape
// assertions require driving the wire directly.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcpext.example/tasks"
)

// rpcError is a decoded JSON-RPC error object.
type rpcError struct {
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// rpcEnvelope is a decoded JSON-RPC response.
type rpcEnvelope struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// newHarness builds the extension + server + wrapped handler around a
// single tool "op" served by handler.
func newHarness(t *testing.T, opts *tasks.Options, handler mcp.ToolHandler) (*httptest.Server, *tasks.Extension) {
	t.Helper()
	ext := tasks.New(opts)
	so := &mcp.ServerOptions{}
	tasks.DeclareServerCapability(so)
	srv := mcp.NewServer(&mcp.Implementation{Name: "tasks-test", Version: "0.0.1"}, so)
	if err := ext.Attach(srv); err != nil {
		t.Fatalf("attach: %v", err)
	}
	srv.AddTool(&mcp.Tool{
		Name:        "op",
		Description: "test operation",
		InputSchema: map[string]any{"type": "object"},
	}, handler)
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	))
	t.Cleanup(ts.Close)
	return ts, ext
}

// textResult builds a plain text CallToolResult.
func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// post sends one JSON-RPC request and decodes the response envelope.
func post(t *testing.T, url string, hdr map[string]string, body string) *rpcEnvelope {
	t.Helper()
	env, _ := postStatus(t, url, hdr, body)
	return env
}

// postStatus is post plus the HTTP status code, for the assertions
// that pin a transport-level rejection alongside the JSON-RPC error.
func postStatus(t *testing.T, url string, hdr map[string]string, body string) (*rpcEnvelope, int) {
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
	defer resp.Body.Close()
	var env rpcEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode response (status %d): %v", resp.StatusCode, err)
	}
	return &env, resp.StatusCode
}

// postRaw sends one request and returns the status and the raw body,
// for the rejections the transport answers in plain text rather than
// as a JSON-RPC envelope.
func postRaw(t *testing.T, url string, hdr map[string]string, body string) (int, string) {
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
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(raw)
}

// metaJSON renders the per-request _meta for the 2026-07-28 protocol,
// declaring the tasks extension when declare is set.
func metaJSON(declare bool) string {
	ext := `{}`
	if declare {
		ext = `{"extensions":{"` + tasks.ExtensionID + `":{}}}`
	}
	return `{"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
		`"io.modelcontextprotocol/clientCapabilities":` + ext + `,` +
		`"io.modelcontextprotocol/clientInfo":{"name":"tasks-probe","version":"0"}}`
}

// callToolBody renders a tools/call request for the "op" tool.
// extraParams (may be empty) is spliced into params as-is, e.g.
// `"inputResponses":{...},"requestState":"s1"`.
func callToolBody(id int, declare bool, extraParams string) string {
	if extraParams != "" {
		extraParams = "," + extraParams
	}
	return fmt.Sprintf(
		`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"op","arguments":{},"_meta":%s%s}}`,
		id, metaJSON(declare), extraParams)
}

// callToolHeaders is the SEP-2243 header set for tools/call.
func callToolHeaders(extra map[string]string) map[string]string {
	hdr := map[string]string{
		"Mcp-Protocol-Version": "2026-07-28",
		"Mcp-Method":           "tools/call",
		"Mcp-Name":             "op",
	}
	for k, v := range extra {
		hdr[k] = v
	}
	return hdr
}

// tasksBody renders a tasks/* request.
func tasksBody(id int, method, taskID string, declare bool, inputResponses string) string {
	params := fmt.Sprintf(`{"taskId":%q,"_meta":%s`, taskID, capsMeta(declare))
	if inputResponses != "" {
		params += `,"inputResponses":` + inputResponses
	}
	params += "}"
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`, id, method, params)
}

// capsMeta renders the _meta carried on tasks/* requests. At protocol
// 2026-07-28 the SDK requires both the protocolVersion (mirroring the
// Mcp-Protocol-Version header) and the clientCapabilities entry on
// every request, so a non-declaring client is one that sends
// capabilities WITHOUT the tasks extension — that is the request the
// extension's own -32003 gate answers.
func capsMeta(declare bool) string {
	exts := `{}`
	if declare {
		exts = `{"extensions":{"` + tasks.ExtensionID + `":{}}}`
	}
	return `{"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
		`"io.modelcontextprotocol/clientCapabilities":` + exts + `}`
}

// tasksHeaders is the SEP-2243 header set for tasks/* methods.
func tasksHeaders(method, taskID string) map[string]string {
	return map[string]string{
		"Mcp-Protocol-Version": "2026-07-28",
		"Mcp-Method":           method,
		"Mcp-Name":             taskID,
	}
}

// emptyAck reports whether an ack carries nothing beyond resultType
// and the "_meta" the SDK attaches to every result it marshals. A
// tasks/update or tasks/cancel ack must leak no task state.
func emptyAck(ack map[string]any) bool {
	for k := range ack {
		if k != "resultType" && k != "_meta" {
			return false
		}
	}
	return true
}

// resultMap decodes the envelope result into a generic map.
func resultMap(t *testing.T, env *rpcEnvelope) map[string]any {
	t.Helper()
	if env.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", env.Error)
	}
	var m map[string]any
	if err := json.Unmarshal(env.Result, &m); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return m
}

// createTask drives a declaring tools/call and returns the decoded
// CreateTaskResult after asserting its shape.
func createTask(t *testing.T, ts *httptest.Server, hdr map[string]string) map[string]any {
	t.Helper()
	env := post(t, ts.URL, callToolHeaders(hdr), callToolBody(1, true, ""))
	res := resultMap(t, env)
	if res["resultType"] != "task" {
		t.Fatalf("resultType = %v, want task (result: %v)", res["resultType"], res)
	}
	id, _ := res["taskId"].(string)
	if !strings.HasPrefix(id, "task_") || len(id) < 20 {
		t.Fatalf("taskId = %q, want unguessable task_ id", id)
	}
	return res
}

// getTask polls tasks/get once.
func getTask(t *testing.T, ts *httptest.Server, taskID string, hdr map[string]string) *rpcEnvelope {
	t.Helper()
	h := tasksHeaders(tasks.MethodGet, taskID)
	for k, v := range hdr {
		h[k] = v
	}
	return post(t, ts.URL, h, tasksBody(2, tasks.MethodGet, taskID, true, ""))
}

// pollUntil polls tasks/get until the task reaches status want.
func pollUntil(t *testing.T, ts *httptest.Server, taskID string, hdr map[string]string, want string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res := resultMap(t, getTask(t, ts, taskID, hdr))
		if res["status"] == want {
			return res
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for task %s to reach %s", taskID, want)
	return nil
}
