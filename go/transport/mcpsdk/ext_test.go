package mcpsdk

// Tests for the full-capability surface: adopter pass-through
// (prompts, resources, templates, subscriptions), pagination,
// runtime Hide/Expose with tools/list_changed, descriptor
// enrichment, progress streaming, and the no-execution-via-
// indirection safety property. All exchanges run through the
// official SDK client over loopback streamable HTTP.

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

const notifyTimeout = 5 * time.Second

// newSurfaceHarness builds a Surface over root, mounts it on a fresh
// router, and returns the test server plus the handle.
func newSurfaceHarness(t *testing.T, root *cobra.Command, bridgeOpts []cmdsurface.Option, opts ...Option) (*httptest.Server, *Surface, *cmdsurface.Bridge) {
	t.Helper()
	b := cmdsurface.New(root, bridgeOpts...)
	s, err := New(b, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r := api.NewRouter()
	if err := s.Mount(r); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, s, b
}

// connectOpts dials with the SDK client using the given client
// options (notification handlers etc.).
func connectOpts(t *testing.T, endpoint string, clientOpts *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "mcpsdk-ext-test", Version: "0.0.1"}, clientOpts)
	sess, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: endpoint}, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func TestAdopterPromptsAndResources(t *testing.T) {
	srv, _, _ := newSurfaceHarness(t, newTestTree(), nil,
		WithServerConfigurator(func(m *mcp.Server) {
			m.AddPrompt(&mcp.Prompt{
				Name:        "greeting",
				Description: "a greeting prompt",
				Arguments:   []*mcp.PromptArgument{{Name: "who", Required: true}},
			}, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{
					{Role: "user", Content: &mcp.TextContent{Text: "hello " + req.Params.Arguments["who"]}},
				}}, nil
			})
			m.AddResource(&mcp.Resource{
				URI: "kit://docs/readme", Name: "readme", MIMEType: "text/plain",
			}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
					{URI: req.Params.URI, MIMEType: "text/plain", Text: "readme body"},
				}}, nil
			})
			m.AddResourceTemplate(&mcp.ResourceTemplate{
				URITemplate: "kit://docs/{page}", Name: "docs",
			}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
					{URI: req.Params.URI, MIMEType: "text/plain", Text: "templated: " + req.Params.URI},
				}}, nil
			})
		}))
	sess := connectOpts(t, srv.URL+"/mcp", nil)

	// Capability advertisement follows from registration (SDK
	// inference): tools from the bridge, prompts + resources from the
	// configurator.
	caps := sess.InitializeResult().Capabilities
	if caps.Tools == nil || caps.Prompts == nil || caps.Resources == nil {
		t.Fatalf("capabilities = tools:%v prompts:%v resources:%v, want all advertised",
			caps.Tools, caps.Prompts, caps.Resources)
	}

	pl, err := sess.ListPrompts(t.Context(), nil)
	if err != nil || len(pl.Prompts) != 1 || pl.Prompts[0].Name != "greeting" {
		t.Fatalf("ListPrompts = %v, %v; want the greeting prompt", pl, err)
	}
	gp, err := sess.GetPrompt(t.Context(), &mcp.GetPromptParams{
		Name: "greeting", Arguments: map[string]string{"who": "kit"},
	})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	if tc, ok := gp.Messages[0].Content.(*mcp.TextContent); !ok || tc.Text != "hello kit" {
		t.Errorf("prompt content = %#v, want hello kit", gp.Messages[0].Content)
	}

	rl, err := sess.ListResources(t.Context(), nil)
	if err != nil || len(rl.Resources) != 1 {
		t.Fatalf("ListResources = %v, %v; want one resource", rl, err)
	}
	rr, err := sess.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "kit://docs/readme"})
	if err != nil || len(rr.Contents) != 1 || rr.Contents[0].Text != "readme body" {
		t.Fatalf("ReadResource = %v, %v; want readme body", rr, err)
	}

	rt, err := sess.ListResourceTemplates(t.Context(), nil)
	if err != nil || len(rt.ResourceTemplates) != 1 {
		t.Fatalf("ListResourceTemplates = %v, %v; want one template", rt, err)
	}
	rr2, err := sess.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "kit://docs/other"})
	if err != nil || len(rr2.Contents) != 1 || rr2.Contents[0].Text != "templated: kit://docs/other" {
		t.Fatalf("templated ReadResource = %v, %v", rr2, err)
	}

	// Tools still fully served alongside.
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "widget.add", Arguments: map[string]any{"name": "x"},
	})
	if err != nil || res.IsError {
		t.Fatalf("widget.add alongside prompts/resources: err=%v isError=%v", err, res != nil && res.IsError)
	}
}

// bigTree returns a root with n leaves spread over groups of ten.
func bigTree(n int) *cobra.Command {
	root := &cobra.Command{Use: "root"}
	var group *cobra.Command
	for i := 0; i < n; i++ {
		if i%10 == 0 {
			group = &cobra.Command{Use: fmt.Sprintf("g%02d", i/10)}
			root.AddCommand(group)
		}
		group.AddCommand(&cobra.Command{
			Use:         fmt.Sprintf("leaf%03d", i),
			Short:       fmt.Sprintf("leaf %03d", i),
			RunE:        func(cmd *cobra.Command, _ []string) error { cmd.Println("ok"); return nil },
			Annotations: map[string]string{"kit/side-effect": "read"},
		})
	}
	return root
}

func TestPagination(t *testing.T) {
	const leaves = 120
	srv, _, _ := newSurfaceHarness(t, bigTree(leaves), nil,
		WithServerOptions(&mcp.ServerOptions{PageSize: 10}))
	sess := connectOpts(t, srv.URL+"/mcp", nil)

	first, err := sess.ListTools(t.Context(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(first.Tools) != 10 {
		t.Errorf("first page = %d tools, want 10", len(first.Tools))
	}
	if first.NextCursor == "" {
		t.Error("first page has no NextCursor")
	}

	// The SDK client's iterator follows cursors; no kit-side cursor
	// logic exists to test — only that every leaf arrives.
	total := 0
	for _, err := range sess.Tools(t.Context(), nil) {
		if err != nil {
			t.Fatalf("Tools iterator: %v", err)
		}
		total++
	}
	if total != leaves {
		t.Errorf("iterator total = %d, want %d", total, leaves)
	}
}

func TestResourceSubscription(t *testing.T) {
	var subscribed atomic.Int32
	srv, s, _ := newSurfaceHarness(t, newTestTree(), nil,
		WithServerOptions(&mcp.ServerOptions{
			SubscribeHandler: func(ctx context.Context, req *mcp.SubscribeRequest) error {
				subscribed.Add(1)
				return nil
			},
			UnsubscribeHandler: func(ctx context.Context, req *mcp.UnsubscribeRequest) error { return nil },
		}),
		WithServerConfigurator(func(m *mcp.Server) {
			m.AddResource(&mcp.Resource{URI: "kit://state", Name: "state", MIMEType: "text/plain"},
				func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
					return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
						{URI: req.Params.URI, MIMEType: "text/plain", Text: "v1"},
					}}, nil
				})
		}))

	updated := make(chan string, 4)
	sess := connectOpts(t, srv.URL+"/mcp", &mcp.ClientOptions{
		ResourceUpdatedHandler: func(ctx context.Context, req *mcp.ResourceUpdatedNotificationRequest) {
			updated <- req.Params.URI
		},
	})

	if caps := sess.InitializeResult().Capabilities; caps.Resources == nil || !caps.Resources.Subscribe {
		t.Fatalf("resources capability = %+v, want subscribe advertised", caps.Resources)
	}
	if err := sess.Subscribe(t.Context(), &mcp.SubscribeParams{URI: "kit://state"}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if got := subscribed.Load(); got != 1 {
		t.Errorf("SubscribeHandler calls = %d, want 1", got)
	}

	if err := s.Server().ResourceUpdated(t.Context(), &mcp.ResourceUpdatedNotificationParams{URI: "kit://state"}); err != nil {
		t.Fatalf("ResourceUpdated: %v", err)
	}
	select {
	case uri := <-updated:
		if uri != "kit://state" {
			t.Errorf("updated uri = %q, want kit://state", uri)
		}
	case <-time.After(notifyTimeout):
		t.Fatal("timed out waiting for resources/updated notification")
	}
}

func TestHideExposeLiveToolList(t *testing.T) {
	srv, s, _ := newSurfaceHarness(t, newTestTree(), nil)

	changed := make(chan struct{}, 8)
	sess := connectOpts(t, srv.URL+"/mcp", &mcp.ClientOptions{
		ToolListChangedHandler: func(ctx context.Context, req *mcp.ToolListChangedRequest) {
			changed <- struct{}{}
		},
	})

	awaitChange := func(step string) {
		t.Helper()
		select {
		case <-changed:
		case <-time.After(notifyTimeout):
			t.Fatalf("timed out waiting for tools/list_changed after %s", step)
		}
	}
	listNames := func() map[string]bool {
		t.Helper()
		res, err := sess.ListTools(t.Context(), nil)
		if err != nil {
			t.Fatalf("ListTools: %v", err)
		}
		names := make(map[string]bool, len(res.Tools))
		for _, tool := range res.Tools {
			names[tool.Name] = true
		}
		return names
	}

	if !listNames()["ping"] {
		t.Fatal("ping missing before Hide")
	}

	// Hide: unlisted, notified, and no longer callable.
	s.Hide("ping")
	awaitChange("Hide")
	if listNames()["ping"] {
		t.Error("ping still listed after Hide")
	}
	if _, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "ping"}); err == nil {
		t.Error("hidden tool still callable")
	}

	// Expose: relisted, notified, callable again.
	s.Expose("ping")
	awaitChange("Expose")
	if !listNames()["ping"] {
		t.Error("ping not listed after Expose")
	}
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "ping"})
	if err != nil || res.IsError {
		t.Fatalf("re-exposed call: err=%v isError=%v", err, res != nil && res.IsError)
	}
	if !strings.Contains(textOf(res), "pong") {
		t.Errorf("re-exposed call text = %q, want pong", textOf(res))
	}
}

func TestSyncAfterDirectBridgeMutation(t *testing.T) {
	srv, s, b := newSurfaceHarness(t, newTestTree(), nil)
	sess := connectOpts(t, srv.URL+"/mcp", nil)

	// Mutating the bridge directly leaves the SDK listing stale until
	// Sync reconciles.
	b.Hide("deploy", cmdsurface.SurfaceMCP)
	s.Sync()

	res, err := sess.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range res.Tools {
		if tool.Name == "deploy" {
			t.Error("deploy still listed after bridge Hide + Sync")
		}
	}
}

// TestExposedDestructiveStaysPolicyBlocked pins that the live-expose
// path cannot bypass the destructive ceiling: exposing a destructive
// leaf lists its tool, but calls still hit the policy gate inside
// Bridge.Invoke.
func TestExposedDestructiveStaysPolicyBlocked(t *testing.T) {
	srv, s, _ := newSurfaceHarness(t, newTestTree(), nil)
	sess := connectOpts(t, srv.URL+"/mcp", nil)

	// Re-expose (already enabled by default; Sync is a no-op) and,
	// for good measure, hide + re-expose to run the full add path.
	s.Hide("widget delete")
	s.Expose("widget delete")

	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "widget.delete"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError || !strings.Contains(textOf(res), "destructive command blocked") {
		t.Errorf("re-exposed destructive: isError=%t text=%q, want policy block", res.IsError, textOf(res))
	}
}

func TestToolDecorator(t *testing.T) {
	outSchema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"status": map[string]any{"type": "string"}},
	}
	srv, _, _ := newSurfaceHarness(t, newTestTree(), nil,
		WithToolDecorator(func(leaf *cmdsurface.Leaf, tool *mcp.Tool) {
			if tool.Name == "ping" {
				tool.Title = "Ping"
				tool.OutputSchema = outSchema
				ro := true
				tool.Annotations.ReadOnlyHint = ro
			}
		}))
	sess := connectOpts(t, srv.URL+"/mcp", nil)

	res, err := sess.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var ping *mcp.Tool
	for _, tool := range res.Tools {
		if tool.Name == "ping" {
			ping = tool
		}
	}
	if ping == nil {
		t.Fatal("ping missing")
	}
	if ping.Title != "Ping" {
		t.Errorf("title = %q, want Ping", ping.Title)
	}
	if ping.OutputSchema == nil {
		t.Error("outputSchema not delivered")
	}
	if ping.Annotations == nil || !ping.Annotations.ReadOnlyHint {
		t.Error("readOnlyHint not delivered")
	}
}

// linesTree returns a tree with one leaf emitting three stdout lines.
func linesTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}
	root.AddCommand(&cobra.Command{
		Use:   "lines",
		Short: "Emit lines",
		RunE: func(cmd *cobra.Command, _ []string) error {
			for i := 1; i <= 3; i++ {
				cmd.Printf("line %d\n", i)
			}
			return nil
		},
		Annotations: map[string]string{"kit/side-effect": "read"},
	})
	return root
}

func TestProgressStreaming(t *testing.T) {
	srv, _, _ := newSurfaceHarness(t, linesTree(), nil)

	type note struct {
		token   any
		message string
	}
	notes := make(chan note, 16)
	sess := connectOpts(t, srv.URL+"/mcp", &mcp.ClientOptions{
		ProgressNotificationHandler: func(ctx context.Context, req *mcp.ProgressNotificationClientRequest) {
			notes <- note{token: req.Params.ProgressToken, message: req.Params.Message}
		},
	})

	params := &mcp.CallToolParams{Name: "lines", Arguments: map[string]any{}}
	params.SetProgressToken("tok-lines")
	res, err := sess.CallTool(t.Context(), params)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError = true, text: %s", textOf(res))
	}
	// Terminal result still carries the full output.
	if got := textOf(res); !strings.Contains(got, "line 1") || !strings.Contains(got, "line 3") {
		t.Errorf("final text = %q, want all lines", got)
	}

	got := make([]string, 0, 3)
	deadline := time.After(notifyTimeout)
	for len(got) < 3 {
		select {
		case n := <-notes:
			if fmt.Sprint(n.token) != "tok-lines" {
				t.Errorf("progress token = %v, want tok-lines", n.token)
			}
			got = append(got, n.message)
		case <-deadline:
			t.Fatalf("timed out: %d/3 progress notifications (%v)", len(got), got)
		}
	}
	for i, want := range []string{"line 1", "line 2", "line 3"} {
		if got[i] != want {
			t.Errorf("progress[%d] = %q, want %q", i, got[i], want)
		}
	}
}

// TestProgressStreamingRespectsGates pins that the streaming path
// applies the same gates as the synchronous one: destructive stays
// policy-blocked, auth stays required, hidden stays uncallable.
func TestProgressStreamingRespectsGates(t *testing.T) {
	srv, s, _ := newSurfaceHarness(t, newTestTree(), nil)
	sess := connectOpts(t, srv.URL+"/mcp", nil)

	call := func(name string) (*mcp.CallToolResult, error) {
		params := &mcp.CallToolParams{Name: name, Arguments: map[string]any{}}
		params.SetProgressToken("tok-gate")
		return sess.CallTool(t.Context(), params)
	}

	res, err := call("widget.delete")
	if err != nil {
		t.Fatalf("destructive stream call: %v", err)
	}
	if !res.IsError || !strings.Contains(textOf(res), "destructive command blocked") {
		t.Errorf("destructive stream: isError=%t text=%q, want policy block", res.IsError, textOf(res))
	}

	res, err = call("secret")
	if err != nil {
		t.Fatalf("auth stream call: %v", err)
	}
	if !res.IsError || !strings.Contains(textOf(res), "authentication required") {
		t.Errorf("auth stream: isError=%t text=%q, want auth block", res.IsError, textOf(res))
	}

	s.Hide("ping")
	if _, err := call("ping"); err == nil {
		t.Error("hidden tool streamable via progress token")
	}
}

// TestNoExecutionViaPromptOrResource pins the indirection safety
// property: nothing this surface registers lets prompts/get or
// resources/read reach a bridge leaf — reading adopter content
// executes zero commands, and the destructive ceiling is unaffected
// by prompts/resources being served.
func TestNoExecutionViaPromptOrResource(t *testing.T) {
	var executions atomic.Int32
	root := &cobra.Command{Use: "root"}
	root.AddCommand(&cobra.Command{
		Use:   "nuke",
		Short: "Destroy everything",
		RunE: func(cmd *cobra.Command, _ []string) error {
			executions.Add(1)
			return nil
		},
		Annotations: map[string]string{"kit/side-effect": "destructive"},
	})

	srv, _, _ := newSurfaceHarness(t, root, nil,
		WithServerConfigurator(func(m *mcp.Server) {
			// Adopter content that *names* the destructive tool; serving
			// it must not invoke anything.
			m.AddPrompt(&mcp.Prompt{Name: "runbook", Description: "mentions nuke"},
				func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
					return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{
						{Role: "user", Content: &mcp.TextContent{Text: "consider calling nuke"}},
					}}, nil
				})
			m.AddResource(&mcp.Resource{URI: "kit://tools/nuke", Name: "nuke-doc", MIMEType: "text/plain"},
				func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
					return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
						{URI: req.Params.URI, MIMEType: "text/plain", Text: "docs for nuke"},
					}}, nil
				})
		}))
	sess := connectOpts(t, srv.URL+"/mcp", nil)

	if _, err := sess.GetPrompt(t.Context(), &mcp.GetPromptParams{Name: "runbook"}); err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	if _, err := sess.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "kit://tools/nuke"}); err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if got := executions.Load(); got != 0 {
		t.Fatalf("prompt/resource reads executed %d command(s), want 0", got)
	}

	// The destructive tool itself remains policy-blocked.
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "nuke"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError || !strings.Contains(textOf(res), "destructive command blocked") {
		t.Errorf("nuke: isError=%t text=%q, want policy block", res.IsError, textOf(res))
	}
	if got := executions.Load(); got != 0 {
		t.Fatalf("blocked destructive call executed %d command(s), want 0", got)
	}
}
