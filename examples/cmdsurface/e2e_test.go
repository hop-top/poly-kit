//go:build e2e

// End-to-end suite for the cmdsurface example. Spins up the example's
// HTTP + RPC servers on ephemeral ports and exercises each surface
// (CLI, REST, RPC unary + stream, MCP list + call) against the live
// listeners. Build-tagged so `go test ./...` ignores it by default; run
// with `go test -tags=e2e -race -count=1 ./examples/cmdsurface/...`.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"hop.top/kit/go/transport/cmdsurface"
)

// liveExample bundles the listeners + app for a single test. start
// constructs a fresh exampleApp per call (the in-process runner
// serialises on a per-bridge mutex, so each test isolates state
// trivially) and binds the app's Router / RPCSrv to ephemeral ports.
type liveExample struct {
	app     *exampleApp
	httpURL string
	rpcURL  string
	httpSrv *http.Server
	rpcSrv  *http.Server
}

func start(t *testing.T) *liveExample {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	app, err := BuildExample(ctx, discardLogger())
	if err != nil {
		cancel()
		t.Fatalf("BuildExample: %v", err)
	}

	httpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		cancel()
		app.Cleanup()
		t.Fatalf("listen http: %v", err)
	}
	rpcLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = httpLis.Close()
		cancel()
		app.Cleanup()
		t.Fatalf("listen rpc: %v", err)
	}

	httpSrv := &http.Server{Handler: app.Router}
	rpcSrv := &http.Server{Handler: app.RPCSrv}

	go func() { _ = httpSrv.Serve(httpLis) }()
	go func() { _ = rpcSrv.Serve(rpcLis) }()

	le := &liveExample{
		app:     app,
		httpURL: "http://" + httpLis.Addr().String(),
		rpcURL:  "http://" + rpcLis.Addr().String(),
		httpSrv: httpSrv,
		rpcSrv:  rpcSrv,
	}
	t.Cleanup(func() {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutCancel()
		_ = httpSrv.Shutdown(shutCtx)
		_ = rpcSrv.Shutdown(shutCtx)
		app.Cleanup()
		cancel()
	})
	return le
}

// discardLogger returns a slog.Logger that swallows output so test
// runs stay quiet (RPC interceptors emit on panic; we keep stdout
// reserved for the test runner).
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- CLI ---

func TestE2E_CLI(t *testing.T) {
	root := buildCobraTree()

	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"widget", "add", "--name", "foo", "--tag", "a", "--tag", "b"})

	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute: %v, stderr=%q", err, stderr.String())
	}
	got := stdout.String()
	if want := "widget add: name=foo tags=[a b]"; !strings.Contains(got, want) {
		t.Errorf("stdout=%q does not contain %q", got, want)
	}
}

// --- REST ---

func TestE2E_RESTHappyPath(t *testing.T) {
	le := start(t)

	body := strings.NewReader(`{"flags":{"name":"foo"}}`)
	req, err := http.NewRequest(http.MethodPost, le.httpURL+"/cmd/widget/add", body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
	var res cmdsurface.Result
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if want := "widget add: name=foo"; !strings.Contains(res.Stdout, want) {
		t.Errorf("Result.Stdout=%q want contains %q", res.Stdout, want)
	}
}

func TestE2E_RESTDestructiveBlocked(t *testing.T) {
	le := start(t)

	// report purge is destructive AND auth-required. The example's
	// allowAnyAuth lets it through auth; the destructive policy then
	// refuses on REST → 403 destructive_blocked. (widget delete is
	// Hide()n entirely from REST, so /cmd/widget/delete is a 404
	// rather than a 403 — we exercise the 403 path explicitly.)
	body := strings.NewReader(`{"flags":{"before":"yesterday"}}`)
	req, err := http.NewRequest(http.MethodPost, le.httpURL+"/cmd/report/purge", body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "destructive_blocked") {
		t.Errorf("body=%s does not contain destructive_blocked", raw)
	}
}

func TestE2E_RESTOpenAPI(t *testing.T) {
	le := start(t)

	resp, err := http.Get(le.httpURL + "/openapi.json")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(raw, []byte("cmd_widget_add")) {
		t.Errorf("OpenAPI spec missing cmd_widget_add operationId; body=%s", raw)
	}
	// widget delete is hidden from REST → its operation must NOT appear.
	if bytes.Contains(raw, []byte("cmd_widget_delete")) {
		t.Errorf("OpenAPI spec unexpectedly contains cmd_widget_delete")
	}
}

// --- RPC ---

func newUnaryRPCClient(baseURL string) *connect.Client[cmdsurface.Invocation, cmdsurface.Result] {
	return connect.NewClient[cmdsurface.Invocation, cmdsurface.Result](
		http.DefaultClient,
		baseURL+cmdsurface.RPCInvokeProcedure,
		cmdsurface.RPCClientOptions()...,
	)
}

func newStreamRPCClient(baseURL string) *connect.Client[cmdsurface.Invocation, cmdsurface.Event] {
	return connect.NewClient[cmdsurface.Invocation, cmdsurface.Event](
		http.DefaultClient,
		baseURL+cmdsurface.RPCInvokeStreamProcedure,
		cmdsurface.RPCClientOptions()...,
	)
}

func TestE2E_RPCHappyPath(t *testing.T) {
	le := start(t)

	client := newUnaryRPCClient(le.rpcURL)
	resp, err := client.CallUnary(context.Background(),
		connect.NewRequest(&cmdsurface.Invocation{
			Path:  []string{"widget", "add"},
			Flags: map[string]any{"name": "foo"},
		}),
	)
	if err != nil {
		t.Fatalf("CallUnary: %v", err)
	}
	if want := "widget add: name=foo"; !strings.Contains(resp.Msg.Stdout, want) {
		t.Errorf("Stdout=%q want contains %q", resp.Msg.Stdout, want)
	}
}

func TestE2E_RPCDestructiveBlocked(t *testing.T) {
	le := start(t)

	// report purge is destructive AND auth-required; supply the auth
	// header so we reach the destructive policy gate rather than the
	// auth gate. The RPC surface should reject with PermissionDenied.
	client := newUnaryRPCClient(le.rpcURL)
	req := connect.NewRequest(&cmdsurface.Invocation{
		Path:  []string{"report", "purge"},
		Flags: map[string]any{"before": "yesterday"},
	})
	req.Header().Set("Authorization", "Bearer test")
	_, err := client.CallUnary(context.Background(), req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got, want := connect.CodeOf(err), connect.CodePermissionDenied; got != want {
		t.Errorf("code=%v want=%v (err=%v)", got, want, err)
	}
}

func TestE2E_RPCStream(t *testing.T) {
	le := start(t)

	client := newStreamRPCClient(le.rpcURL)
	stream, err := client.CallServerStream(context.Background(),
		connect.NewRequest(&cmdsurface.Invocation{Path: []string{"ping"}}),
	)
	if err != nil {
		t.Fatalf("CallServerStream: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	var (
		sawStdout bool
		sawDone   bool
	)
	for stream.Receive() {
		ev := stream.Msg()
		switch ev.Kind {
		case "stdout":
			sawStdout = true
		case "done":
			sawDone = true
		}
	}
	if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("Receive: %v", err)
	}
	if !sawStdout {
		t.Error("did not see any stdout Event")
	}
	if !sawDone {
		t.Error("did not see terminal done Event")
	}
}

// --- MCP ---

type mcpResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func mcpCall(t *testing.T, url string, payload string) mcpResp {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/mcp", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m mcpResp
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return m
}

func TestE2E_MCPToolsList(t *testing.T) {
	le := start(t)

	m := mcpCall(t, le.httpURL,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
	)
	if m.Error != nil {
		t.Fatalf("error: %+v", *m.Error)
	}
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(m.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	names := make(map[string]bool, len(result.Tools))
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}
	// Required entries.
	for _, want := range []string{
		"widget.add", "widget.list", "widget.get", "ping",
		"report.generate",
	} {
		if !names[want] {
			t.Errorf("tools/list missing %q (got %v)", want, names)
		}
	}
	// widget.delete is Hide()n on MCP; must be absent.
	if names["widget.delete"] {
		t.Errorf("tools/list unexpectedly contains widget.delete")
	}
}

func TestE2E_MCPToolsCallHappyPath(t *testing.T) {
	le := start(t)

	m := mcpCall(t, le.httpURL,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call",
		  "params":{"name":"widget.add","arguments":{"name":"foo"}}}`,
	)
	if m.Error != nil {
		t.Fatalf("error: %+v", *m.Error)
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(m.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.IsError {
		t.Errorf("isError=true, result=%+v", result)
	}
	if len(result.Content) == 0 {
		t.Fatal("empty content")
	}
	if want := "widget add: name=foo"; !strings.Contains(result.Content[0].Text, want) {
		t.Errorf("content[0]=%q want contains %q", result.Content[0].Text, want)
	}
}

func TestE2E_MCPToolsCallUnknown(t *testing.T) {
	le := start(t)

	// widget.delete is Hide()n on MCP → tools/call returns JSON-RPC
	// error -32602 (invalid params, unknown tool).
	m := mcpCall(t, le.httpURL,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call",
		  "params":{"name":"widget.delete","arguments":{}}}`,
	)
	if m.Error == nil {
		t.Fatalf("expected JSON-RPC error, got result=%s", m.Result)
	}
	if m.Error.Code != -32602 {
		t.Errorf("code=%d want=-32602 (msg=%q)", m.Error.Code, m.Error.Message)
	}
}
