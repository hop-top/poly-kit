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
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/types/known/structpb"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1/cmdsurfacev1connect"
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
	rpcSrv := &http.Server{Handler: app.RPCSrv, Protocols: app.RPCHTTP.Protocols}

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

// --- REST (the command projection) ---

func TestE2E_RESTHappyPath(t *testing.T) {
	le := start(t)

	body := strings.NewReader(`{"flags":{"name":"foo"}}`)
	req, err := http.NewRequest(http.MethodPost, le.httpURL+"/v1/commands/widget/add", body)
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
	var res api.CommandResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if want := "widget add: name=foo"; !strings.Contains(res.Stdout, want) {
		t.Errorf("Stdout=%q want contains %q", res.Stdout, want)
	}
}

func TestE2E_RESTDestructiveWithheld(t *testing.T) {
	le := start(t)

	// report purge is destructive and REST is not in
	// AllowDestructiveOn, so the projection describes it with the
	// reason and mounts no route for it. widget delete is Hide()n from
	// REST, which discovery reports as withheld-by-config.
	resp, err := http.Get(le.httpURL + "/v1/commands")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("discovery status=%d", resp.StatusCode)
	}
	var doc api.DiscoveryDocument
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	reasons := map[string]string{}
	for _, e := range doc.Commands {
		if !e.Invocable {
			reasons[strings.Join(e.Path, " ")] = e.Reason
		}
	}
	for cmd, want := range map[string]string{
		"report purge":  "unauthorized-destructive",
		"widget delete": cmdsurface.ReasonWithheldByConfig,
	} {
		if got := reasons[cmd]; got != want {
			t.Errorf("%s: reason=%q want %q (withheld=%v)", cmd, got, want, reasons)
		}
	}

	req, err := http.NewRequest(http.MethodPost, le.httpURL+"/v1/commands/report/purge",
		strings.NewReader(`{"flags":{"before":"yesterday"}}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	purge, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	purge.Body.Close()
	if purge.StatusCode != http.StatusNotFound {
		t.Errorf("POST report/purge status=%d, want 404 (no route)", purge.StatusCode)
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
	add := api.OperationIDFor([]string{"widget", "add"})
	if !bytes.Contains(raw, []byte(add)) {
		t.Errorf("OpenAPI spec missing %s operationId; body=%s", add, raw)
	}
	// widget delete is hidden from REST → its operation must NOT appear.
	if del := api.OperationIDFor([]string{"widget", "delete"}); bytes.Contains(raw, []byte(del)) {
		t.Errorf("OpenAPI spec unexpectedly contains %s", del)
	}
}

// --- RPC ---

// newRPCClient returns the generated Commands client speaking native
// gRPC over h2c, the path a non-Go gRPC client takes.
func newRPCClient(baseURL string) cmdsurfacev1connect.CommandsClient {
	h2c := new(http.Protocols)
	h2c.SetUnencryptedHTTP2(true)
	return cmdsurfacev1connect.NewCommandsClient(
		&http.Client{Transport: &http.Transport{Protocols: h2c}},
		baseURL,
		connect.WithGRPC(),
	)
}

// rpcFlags builds an Invocation's flags Struct.
func rpcFlags(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatalf("flags: %v", err)
	}
	return s
}

func TestE2E_RPCHappyPath(t *testing.T) {
	le := start(t)

	client := newRPCClient(le.rpcURL)
	resp, err := client.Invoke(context.Background(),
		connect.NewRequest(&cmdsurfacev1.Invocation{
			Path:  []string{"widget", "add"},
			Flags: rpcFlags(t, map[string]any{"name": "foo"}),
		}),
	)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if want := "widget add: name=foo"; !strings.Contains(resp.Msg.GetStdout(), want) {
		t.Errorf("Stdout=%q want contains %q", resp.Msg.GetStdout(), want)
	}
}

func TestE2E_RPCDestructiveBlocked(t *testing.T) {
	le := start(t)

	// report purge is destructive AND auth-required; supply the auth
	// header so we reach the destructive policy gate rather than the
	// auth gate. The RPC surface should reject with PermissionDenied.
	client := newRPCClient(le.rpcURL)
	req := connect.NewRequest(&cmdsurfacev1.Invocation{
		Path:  []string{"report", "purge"},
		Flags: rpcFlags(t, map[string]any{"before": "yesterday"}),
	})
	req.Header().Set("Authorization", "Bearer test")
	_, err := client.Invoke(context.Background(), req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got, want := connect.CodeOf(err), connect.CodePermissionDenied; got != want {
		t.Errorf("code=%v want=%v (err=%v)", got, want, err)
	}
}

func TestE2E_RPCStream(t *testing.T) {
	le := start(t)

	client := newRPCClient(le.rpcURL)
	stream, err := client.InvokeStream(context.Background(),
		connect.NewRequest(&cmdsurfacev1.Invocation{Path: []string{"ping"}}),
	)
	if err != nil {
		t.Fatalf("InvokeStream: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	var (
		sawStdout bool
		sawDone   bool
	)
	for stream.Receive() {
		ev := stream.Msg()
		switch ev.GetKind() {
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

// --- MCP (the official SDK) ---

// mcpSession connects the official MCP client to the example's /mcp
// endpoint over streamable HTTP.
func mcpSession(t *testing.T, baseURL string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "cmdsurface-e2e", Version: "0.0.1"}, nil)
	sess, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint: baseURL + "/mcp",
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func TestE2E_MCPInitialize(t *testing.T) {
	le := start(t)

	sess := mcpSession(t, le.httpURL)
	info := sess.InitializeResult().ServerInfo
	if info == nil || info.Name != "cmdsurface-example" {
		t.Errorf("serverInfo=%+v, want cmdsurface-example", info)
	}
}

func TestE2E_MCPToolsList(t *testing.T) {
	le := start(t)

	res, err := mcpSession(t, le.httpURL).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	names := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
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

	res, err := mcpSession(t, le.httpURL).CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "widget.add",
		Arguments: map[string]any{"name": "foo"},
	})
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	if res.IsError {
		t.Errorf("isError=true, result=%+v", res)
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	if want := "widget add: name=foo"; !strings.Contains(text.String(), want) {
		t.Errorf("content=%q want contains %q", text.String(), want)
	}
}

func TestE2E_MCPToolsCallUnknown(t *testing.T) {
	le := start(t)

	// widget.delete is Hide()n on MCP, so it is no tool: the call is a
	// JSON-RPC error, not an isError result.
	_, err := mcpSession(t, le.httpURL).CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "widget.delete",
		Arguments: map[string]any{},
	})
	if err == nil {
		t.Fatal("tools/call widget.delete succeeded; want an unknown-tool error")
	}
	if !strings.Contains(err.Error(), "widget.delete") {
		t.Errorf("err=%v does not name the tool", err)
	}
}
