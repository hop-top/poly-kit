package mcpsdk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// newTestTree builds the canonical tree used by the surface tests:
//
//	root
//	├── widget
//	│   ├── add     (write; flags: name str req, count int, force bool, tag []str, hidden str hidden)
//	│   └── delete  (destructive)
//	├── secret      (auth-required)
//	├── deploy      (requires-confirmation)
//	└── ping        (read)
func newTestTree() *cobra.Command {
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
		Use:   "secret",
		Short: "Locked",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("unlocked")
			return nil
		},
		Annotations: map[string]string{"kit/auth-required": "true"},
	}
	root.AddCommand(secret)

	deploy := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("deployed")
			return nil
		},
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

// newHarness mounts the bridge on a fresh api.Router via Mount and
// returns the httptest server plus the bridge for post-mount
// mutation. build customizes bridge construction.
func newHarness(t *testing.T, build func(root *cobra.Command) *cmdsurface.Bridge, opts ...Option) (*httptest.Server, *cmdsurface.Bridge) {
	t.Helper()
	root := newTestTree()
	b := build(root)
	r := api.NewRouter()
	if err := Mount(b, r, opts...); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, b
}

func defaultBridge(root *cobra.Command) *cmdsurface.Bridge {
	return cmdsurface.New(root)
}

// headerTransport injects fixed headers into every outbound request.
type headerTransport struct {
	base http.RoundTripper
	hdr  map[string]string
}

func (ht headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range ht.hdr {
		req.Header.Set(k, v)
	}
	return ht.base.RoundTrip(req)
}

// connect dials the harness with the official SDK client, optionally
// injecting extra HTTP headers on every request.
func connect(t *testing.T, endpoint string, hdr map[string]string) *mcp.ClientSession {
	t.Helper()
	hc := &http.Client{}
	if len(hdr) > 0 {
		hc.Transport = headerTransport{base: http.DefaultTransport, hdr: hdr}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mcpsdk-test", Version: "0.0.1"}, nil)
	sess, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   endpoint,
		HTTPClient: hc,
	}, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// textOf joins all text content blocks of a result.
func textOf(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestListTools(t *testing.T) {
	srv, _ := newHarness(t, defaultBridge)
	sess := connect(t, srv.URL+"/mcp", nil)

	res, err := sess.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	byName := make(map[string]*mcp.Tool, len(res.Tools))
	for _, tool := range res.Tools {
		byName[tool.Name] = tool
	}
	for _, want := range []string{"widget.add", "widget.delete", "secret", "deploy", "ping"} {
		if byName[want] == nil {
			t.Errorf("tool %q missing from listing", want)
		}
	}

	add := byName["widget.add"]
	if add == nil {
		t.Fatal("widget.add missing")
	}
	if add.Description != "Add a widget" {
		t.Errorf("description = %q, want %q", add.Description, "Add a widget")
	}
	schema, ok := add.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("InputSchema type = %T, want map", add.InputSchema)
	}
	props, _ := schema["properties"].(map[string]any)
	for _, want := range []string{"name", "count", "force", "tag"} {
		if props[want] == nil {
			t.Errorf("schema property %q missing", want)
		}
	}
	for _, banned := range []string{"hidden-flag", "deprecated-flag"} {
		if props[banned] != nil {
			t.Errorf("schema property %q should be excluded", banned)
		}
	}
	count, _ := props["count"].(map[string]any)
	if got := count["type"]; got != "integer" {
		t.Errorf("count type = %v, want integer", got)
	}
	required, _ := schema["required"].([]any)
	if len(required) != 1 || required[0] != "name" {
		t.Errorf("required = %v, want [name]", required)
	}

	del := byName["widget.delete"]
	if del.Annotations == nil || del.Annotations.DestructiveHint == nil || !*del.Annotations.DestructiveHint {
		t.Error("widget.delete should carry destructiveHint=true")
	}
	if add.Annotations == nil || add.Annotations.DestructiveHint == nil || *add.Annotations.DestructiveHint {
		t.Error("widget.add should carry destructiveHint=false")
	}
}

func TestCallTool(t *testing.T) {
	srv, _ := newHarness(t, defaultBridge)
	sess := connect(t, srv.URL+"/mcp", nil)

	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "widget.add",
		Arguments: map[string]any{"name": "sprocket", "count": 2},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError = true, text: %s", textOf(res))
	}
	if got := textOf(res); !strings.Contains(got, "added") {
		t.Errorf("text = %q, want to contain %q", got, "added")
	}
}

func TestCallToolMissingRequiredFlag(t *testing.T) {
	srv, _ := newHarness(t, defaultBridge)
	sess := connect(t, srv.URL+"/mcp", nil)

	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "widget.add",
		Arguments: map[string]any{"count": 1},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("IsError = false, want true for missing required flag")
	}
	if got := textOf(res); !strings.Contains(got, "required flag") {
		t.Errorf("text = %q, want to mention the required flag", got)
	}
}

func TestDestructiveBlockedByDefault(t *testing.T) {
	srv, _ := newHarness(t, defaultBridge)
	sess := connect(t, srv.URL+"/mcp", nil)

	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "widget.delete"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("IsError = false, want true for policy-blocked destructive leaf")
	}
	if got := textOf(res); !strings.Contains(got, "destructive command blocked") {
		t.Errorf("text = %q, want destructive-block message", got)
	}
}

func TestDestructiveAllowedByPolicy(t *testing.T) {
	srv, _ := newHarness(t, func(root *cobra.Command) *cmdsurface.Bridge {
		return cmdsurface.New(root, cmdsurface.WithPolicy(cmdsurface.Policy{
			AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceMCP},
		}))
	})
	sess := connect(t, srv.URL+"/mcp", nil)

	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "widget.delete"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError = true, text: %s", textOf(res))
	}
	if got := textOf(res); !strings.Contains(got, "deleted") {
		t.Errorf("text = %q, want to contain %q", got, "deleted")
	}
}

func TestAuthGate(t *testing.T) {
	srv, _ := newHarness(t, defaultBridge)

	// Without Authorization: blocked.
	sess := connect(t, srv.URL+"/mcp", nil)
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "secret"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError || !strings.Contains(textOf(res), "authentication required") {
		t.Errorf("unauthenticated call: isError=%t text=%q, want auth block", res.IsError, textOf(res))
	}

	// With Authorization: allowed.
	authed := connect(t, srv.URL+"/mcp", map[string]string{"Authorization": "Bearer token"})
	res, err = authed.CallTool(t.Context(), &mcp.CallToolParams{Name: "secret"})
	if err != nil {
		t.Fatalf("CallTool (authed): %v", err)
	}
	if res.IsError {
		t.Fatalf("authed call IsError = true, text: %s", textOf(res))
	}
	if got := textOf(res); !strings.Contains(got, "unlocked") {
		t.Errorf("text = %q, want to contain %q", got, "unlocked")
	}
}

func TestConfirmationGate(t *testing.T) {
	srv, _ := newHarness(t, defaultBridge)

	sess := connect(t, srv.URL+"/mcp", nil)
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "deploy"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError || !strings.Contains(textOf(res), "confirmation required") {
		t.Errorf("unconfirmed call: isError=%t text=%q, want confirmation block", res.IsError, textOf(res))
	}

	confirmed := connect(t, srv.URL+"/mcp", map[string]string{"X-Confirm-Token": "yes"})
	res, err = confirmed.CallTool(t.Context(), &mcp.CallToolParams{Name: "deploy"})
	if err != nil {
		t.Fatalf("CallTool (confirmed): %v", err)
	}
	if res.IsError {
		t.Fatalf("confirmed call IsError = true, text: %s", textOf(res))
	}
}

func TestUnknownTool(t *testing.T) {
	srv, _ := newHarness(t, defaultBridge)
	sess := connect(t, srv.URL+"/mcp", nil)

	if _, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "nope"}); err == nil {
		t.Fatal("CallTool(nope) succeeded, want protocol error")
	}
}

func TestHiddenAfterMountFailsClosed(t *testing.T) {
	srv, b := newHarness(t, defaultBridge)
	sess := connect(t, srv.URL+"/mcp", nil)

	// Sanity: callable before hiding.
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "ping"})
	if err != nil || res.IsError {
		t.Fatalf("pre-hide call failed: err=%v isError=%v", err, res != nil && res.IsError)
	}

	b.Hide("ping", cmdsurface.SurfaceMCP)
	if _, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "ping"}); err == nil {
		t.Fatal("post-hide call succeeded, want protocol error")
	}
}

// dataRunner returns a fixed Result carrying structured Data,
// exercising the structuredContent path end to end.
type dataRunner struct{ res cmdsurface.Result }

func (r dataRunner) Run(context.Context, cmdsurface.Invocation) (cmdsurface.Result, error) {
	return r.res, nil
}

func (r dataRunner) Stream(_ context.Context, _ cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
	close(out)
	return nil
}

func TestStructuredContent(t *testing.T) {
	srv, _ := newHarness(t, func(root *cobra.Command) *cmdsurface.Bridge {
		return cmdsurface.New(root, cmdsurface.WithRunner(dataRunner{res: cmdsurface.Result{
			Stdout: "ok",
			Data:   map[string]any{"id": "w-1", "count": float64(3)},
		}}))
	})
	sess := connect(t, srv.URL+"/mcp", nil)

	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "ping"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError = true, text: %s", textOf(res))
	}
	sc, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent type = %T, want map", res.StructuredContent)
	}
	if sc["id"] != "w-1" {
		t.Errorf("structuredContent id = %v, want w-1", sc["id"])
	}
}

func TestInMemoryTransport(t *testing.T) {
	b := cmdsurface.New(newTestTree())
	srv, err := NewServer(b, WithServerInfo("kit-test", "1.2.3"))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(t.Context(), serverT, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mem-test", Version: "0"}, nil)
	sess, err := client.Connect(t.Context(), clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer sess.Close()

	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "ping"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError || !strings.Contains(textOf(res), "pong") {
		t.Errorf("ping over in-memory: isError=%t text=%q", res.IsError, textOf(res))
	}

	// No HTTP headers exist on this transport: header-gated leaves
	// must fail closed.
	res, err = sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "secret"})
	if err != nil {
		t.Fatalf("CallTool(secret): %v", err)
	}
	if !res.IsError || !strings.Contains(textOf(res), "authentication required") {
		t.Errorf("headerless auth leaf: isError=%t text=%q, want fail closed", res.IsError, textOf(res))
	}
}

func TestMountCustomPath(t *testing.T) {
	srv, _ := newHarness(t, defaultBridge, WithPath("/tools/mcp"))
	sess := connect(t, srv.URL+"/tools/mcp", nil)

	if _, err := sess.ListTools(t.Context(), nil); err != nil {
		t.Fatalf("ListTools on custom path: %v", err)
	}
}

func TestNilInputs(t *testing.T) {
	if _, err := NewServer(nil); err == nil {
		t.Error("NewServer(nil) succeeded, want error")
	}
	if _, err := Handler(nil); err == nil {
		t.Error("Handler(nil) succeeded, want error")
	}
	if err := Mount(nil, api.NewRouter()); err == nil {
		t.Error("Mount(nil bridge) succeeded, want error")
	}
	if err := Mount(cmdsurface.New(&cobra.Command{Use: "r"}), nil); err == nil {
		t.Error("Mount(nil router) succeeded, want error")
	}
}
