package cmdsurface

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
)

// newMCPHarness builds a bridge over a fresh test tree, exposes the
// MCP surface on a fresh api.Router, and returns the assembled test
// server. Callers customize the tree / bridge / runner via the
// supplied builder funcs.
func newMCPHarness(t *testing.T, build func(root *cobra.Command) (*Bridge, error)) *httptest.Server {
	t.Helper()
	root := newMCPTestTree()
	b, err := build(root)
	if err != nil {
		t.Fatalf("harness build: %v", err)
	}
	r := api.NewRouter()
	if err := MountMCP(b, r); err != nil {
		t.Fatalf("MountMCP: %v", err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// newMCPTestTree builds the canonical tree used by the MCP surface
// tests:
//
//	root
//	├── widget
//	│   ├── add     (write; flags: name str req, count int, force bool, tag []str, hidden str hidden)
//	│   └── delete  (destructive)
//	├── secret      (auth-required)
//	├── deploy      (requires-confirmation)
//	└── ping        (read)
func newMCPTestTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}

	widget := &cobra.Command{Use: "widget"}
	add := &cobra.Command{
		Use:   "add",
		Short: "Add a widget",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("added")
			return nil
		},
		Annotations: map[string]string{"kit/side-effect": "write"},
	}
	add.Flags().String("name", "", "widget name")
	add.Flags().Int("count", 0, "widget count")
	add.Flags().Bool("force", false, "force flag")
	add.Flags().StringSlice("tag", nil, "tag list")
	add.Flags().String("hidden-flag", "", "should be hidden")
	_ = add.Flags().MarkHidden("hidden-flag")
	add.Flags().String("deprecated-flag", "", "should be dropped")
	_ = add.Flags().MarkDeprecated("deprecated-flag", "old")
	_ = add.MarkFlagRequired("name")
	widget.AddCommand(add)

	del := &cobra.Command{
		Use:   "delete",
		Short: "Delete a widget",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("deleted")
			return nil
		},
		Annotations: map[string]string{"kit/side-effect": "destructive"},
	}
	widget.AddCommand(del)
	root.AddCommand(widget)

	secret := &cobra.Command{
		Use:         "secret",
		Short:       "Locked",
		RunE:        func(cmd *cobra.Command, _ []string) error { return nil },
		Annotations: map[string]string{"kit/auth-required": "true"},
	}
	root.AddCommand(secret)

	deploy := &cobra.Command{
		Use:         "deploy",
		Short:       "Deploy",
		RunE:        func(cmd *cobra.Command, _ []string) error { return nil },
		Annotations: map[string]string{"kit/requires-confirmation": "true"},
	}
	root.AddCommand(deploy)

	ping := &cobra.Command{
		Use:   "ping",
		Short: "Ping the server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("pong")
			return nil
		},
		Annotations: map[string]string{"kit/side-effect": "read"},
	}
	root.AddCommand(ping)

	return root
}

// postRPC sends a JSON-RPC request and returns the decoded response
// + HTTP status code.
func postRPC(t *testing.T, srv *httptest.Server, headers map[string]string, body any) (int, jsonRPCResponse) {
	t.Helper()
	enc, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", bytes.NewReader(enc))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var decoded jsonRPCResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal response (%s): %v", string(raw), err)
	}
	return resp.StatusCode, decoded
}

// resultAsMap reads the Result field as a generic map. Returns nil if
// the response carried no result.
func resultAsMap(t *testing.T, resp jsonRPCResponse) map[string]any {
	t.Helper()
	if resp.Result == nil {
		return nil
	}
	// jsonRPCResponse.Result is any (decoded into map[string]any
	// already since we round-tripped through json.Unmarshal into a
	// struct field typed as any).
	m, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result not an object: %T", resp.Result)
	}
	return m
}

func TestMCP_Initialize(t *testing.T) {
	srv := newMCPHarness(t, func(root *cobra.Command) (*Bridge, error) {
		return New(root), nil
	})

	status, resp := postRPC(t, srv, nil, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	})
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200", status)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	m := resultAsMap(t, resp)
	if got := m["protocolVersion"]; got != mcpProtocolVersion {
		t.Errorf("protocolVersion=%v want=%v", got, mcpProtocolVersion)
	}
	caps, _ := m["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Errorf("capabilities.tools missing: %v", caps)
	}
	info, _ := m["serverInfo"].(map[string]any)
	if info["name"] != defaultMCPServerName {
		t.Errorf("serverInfo.name=%v want=%v", info["name"], defaultMCPServerName)
	}
	if info["version"] != defaultMCPServerVersion {
		t.Errorf("serverInfo.version=%v want=%v", info["version"], defaultMCPServerVersion)
	}
}

func TestMCP_ToolsList_HappyPath(t *testing.T) {
	srv := newMCPHarness(t, func(root *cobra.Command) (*Bridge, error) {
		return New(root), nil
	})

	_, resp := postRPC(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	m := resultAsMap(t, resp)
	tools, _ := m["tools"].([]any)
	names := toolNames(tools)
	want := []string{"widget.add", "widget.delete", "secret", "deploy", "ping"}
	if !sameSet(names, want) {
		t.Errorf("tool names=%v want set=%v", names, want)
	}
}

func TestMCP_ToolsList_FiltersDisabled(t *testing.T) {
	srv := newMCPHarness(t, func(root *cobra.Command) (*Bridge, error) {
		return New(root).Hide("widget delete", SurfaceMCP), nil
	})

	_, resp := postRPC(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	m := resultAsMap(t, resp)
	tools, _ := m["tools"].([]any)
	for _, tool := range tools {
		tm, _ := tool.(map[string]any)
		if tm["name"] == "widget.delete" {
			t.Errorf("widget.delete should be filtered: %v", tm)
		}
	}
}

func TestMCP_ToolsList_FlagTypes(t *testing.T) {
	srv := newMCPHarness(t, func(root *cobra.Command) (*Bridge, error) {
		return New(root), nil
	})
	_, resp := postRPC(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	add := findTool(t, resp, "widget.add")
	props, _ := add["inputSchema"].(map[string]any)["properties"].(map[string]any)

	cases := []struct {
		flag string
		typ  string
		arr  bool
	}{
		{"name", "string", false},
		{"count", "integer", false},
		{"force", "boolean", false},
		{"tag", "array", true},
	}
	for _, c := range cases {
		p, ok := props[c.flag].(map[string]any)
		if !ok {
			t.Errorf("missing property %q", c.flag)
			continue
		}
		if p["type"] != c.typ {
			t.Errorf("flag %q type=%v want=%v", c.flag, p["type"], c.typ)
		}
		if c.arr {
			items, _ := p["items"].(map[string]any)
			if items["type"] != "string" {
				t.Errorf("flag %q items.type=%v want=string", c.flag, items["type"])
			}
		}
	}
}

func TestMCP_ToolsList_RequiredFlags(t *testing.T) {
	srv := newMCPHarness(t, func(root *cobra.Command) (*Bridge, error) {
		return New(root), nil
	})
	_, resp := postRPC(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	add := findTool(t, resp, "widget.add")
	schema, _ := add["inputSchema"].(map[string]any)
	required, _ := schema["required"].([]any)
	var got []string
	for _, r := range required {
		s, _ := r.(string)
		got = append(got, s)
	}
	if !sameSet(got, []string{"name"}) {
		t.Errorf("required=%v want=[name]", got)
	}
}

func TestMCP_ToolsList_HiddenFilter(t *testing.T) {
	srv := newMCPHarness(t, func(root *cobra.Command) (*Bridge, error) {
		return New(root), nil
	})
	_, resp := postRPC(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	add := findTool(t, resp, "widget.add")
	props, _ := add["inputSchema"].(map[string]any)["properties"].(map[string]any)
	if _, ok := props["hidden-flag"]; ok {
		t.Errorf("hidden-flag must be excluded: %v", props)
	}
	if _, ok := props["deprecated-flag"]; ok {
		t.Errorf("deprecated-flag must be excluded: %v", props)
	}
}

func TestMCP_ToolsCall_HappyPath(t *testing.T) {
	srv := newMCPHarness(t, func(root *cobra.Command) (*Bridge, error) {
		return New(root, WithRunner(&fakeRunner{
			run: func(_ context.Context, _ Invocation) (Result, error) {
				return Result{Stdout: "pong\n"}, nil
			},
		})), nil
	})

	_, resp := postRPC(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "ping"},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	m := resultAsMap(t, resp)
	if m["isError"] != false {
		t.Errorf("isError=%v want=false", m["isError"])
	}
	content := m["content"].([]any)
	first := content[0].(map[string]any)
	if first["type"] != "text" || !strings.Contains(first["text"].(string), "pong") {
		t.Errorf("content[0]=%v want text with pong", first)
	}
}

func TestMCP_ToolsCall_UnknownTool(t *testing.T) {
	srv := newMCPHarness(t, func(root *cobra.Command) (*Bridge, error) {
		return New(root), nil
	})
	_, resp := postRPC(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "nope.nada"},
	})
	if resp.Error == nil || resp.Error.Code != mcpErrInvalidParams {
		t.Errorf("err=%+v want code=%d", resp.Error, mcpErrInvalidParams)
	}
}

func TestMCP_ToolsCall_SurfaceNotEnabled(t *testing.T) {
	srv := newMCPHarness(t, func(root *cobra.Command) (*Bridge, error) {
		return New(root).Hide("ping", SurfaceMCP), nil
	})
	_, resp := postRPC(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "ping"},
	})
	if resp.Error == nil || resp.Error.Code != mcpErrInvalidParams {
		t.Errorf("err=%+v want code=%d", resp.Error, mcpErrInvalidParams)
	}
}

func TestMCP_ToolsCall_DestructiveBlocked(t *testing.T) {
	srv := newMCPHarness(t, func(root *cobra.Command) (*Bridge, error) {
		// Default policy: no surfaces in AllowDestructiveOn → MCP-side
		// destructive is blocked.
		return New(root), nil
	})
	status, resp := postRPC(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "widget.delete"},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", resp.Error)
	}
	if status != http.StatusOK {
		t.Errorf("status=%d want=200", status)
	}
	m := resultAsMap(t, resp)
	if m["isError"] != true {
		t.Errorf("isError=%v want=true", m["isError"])
	}
}

func TestMCP_ToolsCall_DestructiveAllowed(t *testing.T) {
	srv := newMCPHarness(t, func(root *cobra.Command) (*Bridge, error) {
		return New(root,
			WithPolicy(Policy{
				AllowDestructiveOn: []Surface{SurfaceMCP},
				DefaultEnabled:     []Surface{SurfaceCLI, SurfaceLib, SurfaceMCP},
			}),
			WithRunner(&fakeRunner{
				run: func(_ context.Context, _ Invocation) (Result, error) {
					return Result{Stdout: "deleted\n"}, nil
				},
			}),
		), nil
	})
	_, resp := postRPC(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "widget.delete"},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	m := resultAsMap(t, resp)
	if m["isError"] != false {
		t.Errorf("isError=%v want=false", m["isError"])
	}
}

func TestMCP_ToolsCall_AuthRequired(t *testing.T) {
	srv := newMCPHarness(t, func(root *cobra.Command) (*Bridge, error) {
		return New(root, WithRunner(&fakeRunner{
			run: func(_ context.Context, _ Invocation) (Result, error) {
				return Result{}, nil
			},
		})), nil
	})
	status, resp := postRPC(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "secret"},
	})
	if status != http.StatusUnauthorized {
		t.Errorf("status=%d want=401", status)
	}
	m := resultAsMap(t, resp)
	if m["isError"] != true {
		t.Errorf("isError=%v want=true", m["isError"])
	}

	// With Authorization header, the call proceeds.
	status, resp = postRPC(t, srv, map[string]string{"Authorization": "Bearer x"}, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "secret"},
	})
	if status != http.StatusOK {
		t.Errorf("status with auth=%d want=200", status)
	}
	m = resultAsMap(t, resp)
	if m["isError"] != false {
		t.Errorf("isError with auth=%v want=false", m["isError"])
	}
}

func TestMCP_ToolsCall_ConfirmRequired(t *testing.T) {
	srv := newMCPHarness(t, func(root *cobra.Command) (*Bridge, error) {
		return New(root, WithRunner(&fakeRunner{
			run: func(_ context.Context, _ Invocation) (Result, error) {
				return Result{}, nil
			},
		})), nil
	})
	status, resp := postRPC(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "deploy"},
	})
	if status != http.StatusPreconditionRequired {
		t.Errorf("status=%d want=428", status)
	}
	m := resultAsMap(t, resp)
	if m["isError"] != true {
		t.Errorf("isError=%v want=true", m["isError"])
	}

	status, resp = postRPC(t, srv, map[string]string{"X-Confirm-Token": "yes"}, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "deploy"},
	})
	if status != http.StatusOK {
		t.Errorf("status with confirm=%d want=200", status)
	}
	m = resultAsMap(t, resp)
	if m["isError"] != false {
		t.Errorf("isError with confirm=%v want=false", m["isError"])
	}
}

func TestMCP_ToolsCall_StderrBlock(t *testing.T) {
	srv := newMCPHarness(t, func(root *cobra.Command) (*Bridge, error) {
		return New(root, WithRunner(&fakeRunner{
			run: func(_ context.Context, _ Invocation) (Result, error) {
				return Result{Stdout: "ok", Stderr: "warning"}, nil
			},
		})), nil
	})
	_, resp := postRPC(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "ping"},
	})
	m := resultAsMap(t, resp)
	content := m["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content len=%d want=2: %v", len(content), content)
	}
	first := content[0].(map[string]any)
	if first["text"] != "ok" {
		t.Errorf("content[0].text=%v want=ok", first["text"])
	}
	second := content[1].(map[string]any)
	if !strings.HasPrefix(second["text"].(string), "[stderr] ") ||
		!strings.Contains(second["text"].(string), "warning") {
		t.Errorf("content[1]=%v want [stderr] warning", second)
	}
}

func TestMCP_ToolsCall_ExitCode(t *testing.T) {
	srv := newMCPHarness(t, func(root *cobra.Command) (*Bridge, error) {
		return New(root, WithRunner(&fakeRunner{
			run: func(_ context.Context, _ Invocation) (Result, error) {
				return Result{Stdout: "boom", ExitCode: 2}, nil
			},
		})), nil
	})
	status, resp := postRPC(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "ping"},
	})
	if status != http.StatusOK {
		t.Errorf("status=%d want=200", status)
	}
	m := resultAsMap(t, resp)
	if m["isError"] != true {
		t.Errorf("isError=%v want=true (ExitCode=2)", m["isError"])
	}
}

func TestMCP_ToolsCall_DataField(t *testing.T) {
	srv := newMCPHarness(t, func(root *cobra.Command) (*Bridge, error) {
		return New(root, WithRunner(&fakeRunner{
			run: func(_ context.Context, _ Invocation) (Result, error) {
				return Result{
					Stdout: "ok",
					Data:   map[string]any{"id": 42, "name": "x"},
				}, nil
			},
		})), nil
	})
	_, resp := postRPC(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "ping"},
	})
	m := resultAsMap(t, resp)
	content := m["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content len=%d want=2: %v", len(content), content)
	}
	dataBlock := content[1].(map[string]any)
	text, _ := dataBlock["text"].(string)
	var roundTrip map[string]any
	if err := json.Unmarshal([]byte(text), &roundTrip); err != nil {
		t.Fatalf("data block not valid JSON: %q: %v", text, err)
	}
	if id, _ := roundTrip["id"].(float64); int(id) != 42 {
		t.Errorf("data.id=%v want=42", roundTrip["id"])
	}
}

func TestMCP_ToolsCall_ForcesMetaSurface(t *testing.T) {
	captured := make(chan Invocation, 1)
	srv := newMCPHarness(t, func(root *cobra.Command) (*Bridge, error) {
		return New(root, WithRunner(&fakeRunner{
			run: func(_ context.Context, inv Invocation) (Result, error) {
				captured <- inv
				return Result{}, nil
			},
		})), nil
	})
	_, resp := postRPC(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "ping",
			"arguments": map[string]any{"verbose": "true"},
		},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
	inv := <-captured
	if inv.Meta.Surface != SurfaceMCP {
		t.Errorf("Meta.Surface=%v want=%v", inv.Meta.Surface, SurfaceMCP)
	}
	if inv.Flags["verbose"] != "true" {
		t.Errorf("Flags[verbose]=%v want=true", inv.Flags["verbose"])
	}
}

func TestMCP_UnknownMethod(t *testing.T) {
	srv := newMCPHarness(t, func(root *cobra.Command) (*Bridge, error) {
		return New(root), nil
	})
	_, resp := postRPC(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "nope/anywhere",
	})
	if resp.Error == nil || resp.Error.Code != mcpErrMethodNotFound {
		t.Errorf("err=%+v want code=%d", resp.Error, mcpErrMethodNotFound)
	}
}

// toolNames extracts the name field from a tools/list result array.
func toolNames(tools []any) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		tm, _ := t.(map[string]any)
		if name, _ := tm["name"].(string); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// findTool locates the named tool envelope in a tools/list result.
// Fails the test if not present.
func findTool(t *testing.T, resp jsonRPCResponse, name string) map[string]any {
	t.Helper()
	m, _ := resp.Result.(map[string]any)
	tools, _ := m["tools"].([]any)
	for _, tool := range tools {
		tm, _ := tool.(map[string]any)
		if tm["name"] == name {
			return tm
		}
	}
	t.Fatalf("tool %q not found in %v", name, tools)
	return nil
}

// sameSet reports whether a and b contain the same elements (order
// irrelevant, duplicates compared by count).
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sa := append([]string(nil), a...)
	sb := append([]string(nil), b...)
	sort.Strings(sa)
	sort.Strings(sb)
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}
