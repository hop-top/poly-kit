package mcpsdk

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hop.top/kit/go/console/cli/idemstore"
	"hop.top/kit/go/transport/cmdsurface"
)

// ordinalRunner answers each run with its ordinal.
type ordinalRunner struct{ calls atomic.Int32 }

func (r *ordinalRunner) Run(context.Context, cmdsurface.Invocation) (cmdsurface.Result, error) {
	return cmdsurface.Result{Stdout: fmt.Sprintf("run %d", r.calls.Add(1))}, nil
}

func (r *ordinalRunner) Stream(context.Context, cmdsurface.Invocation, chan<- cmdsurface.Event) error {
	return nil
}

func idemOpts(run cmdsurface.Runner) []cmdsurface.Option {
	return []cmdsurface.Option{
		cmdsurface.WithRunner(run),
		cmdsurface.WithIdempotency(cmdsurface.NewIdempotencyLedger(idemstore.Memory()), time.Hour),
	}
}

func keyedCall(t *testing.T, sess *mcp.ClientSession, tool, key string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{
		Meta:      mcp.Meta{cmdsurface.MCPMetaIdempotencyKey: key},
		Name:      tool,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", tool, err)
	}
	return res
}

func TestIdempotencyKeyInMetaReplays(t *testing.T) {
	run := &ordinalRunner{}
	srv, _, _ := newSurfaceHarness(t, newTestTree(), idemOpts(run))
	sess := connect(t, srv.URL+"/mcp", nil)

	first := keyedCall(t, sess, "widget.add", "k1", map[string]any{"name": "a"})
	if first.IsError || first.Meta[cmdsurface.MCPMetaIdempotentReplayed] != nil {
		t.Fatalf("first call: %q meta %v", textOf(first), first.Meta)
	}
	second := keyedCall(t, sess, "widget.add", "k1", map[string]any{"name": "a"})
	if second.Meta[cmdsurface.MCPMetaIdempotentReplayed] != true || textOf(second) != textOf(first) {
		t.Fatalf("replay: %q meta %v, want %q marked replayed", textOf(second), second.Meta, textOf(first))
	}
	if run.calls.Load() != 1 {
		t.Fatalf("runs = %d, want 1", run.calls.Load())
	}

	reused := keyedCall(t, sess, "widget.add", "k1", map[string]any{"name": "b"})
	if !reused.IsError || !strings.HasPrefix(textOf(reused), cmdsurface.CodeIdempotencyKeyReused) {
		t.Fatalf("reused key: isError=%t text=%q, want it to start with %s",
			reused.IsError, textOf(reused), cmdsurface.CodeIdempotencyKeyReused)
	}
	refusal, _ := reused.Meta[cmdsurface.MCPRefusalMetaKey].(map[string]any)
	if refusal["code"] != cmdsurface.CodeIdempotencyKeyReused {
		t.Fatalf("refusal _meta = %v", reused.Meta)
	}
}

// TestIdempotencyKeyHeaderAndMeta: the Idempotency-Key header carries
// the key on HTTP, and a _meta key wins over it.
func TestIdempotencyKeyHeaderAndMeta(t *testing.T) {
	run := &ordinalRunner{}
	srv, _, _ := newSurfaceHarness(t, newTestTree(), idemOpts(run),
		WithCallMeta(func(context.Context, *mcp.CallToolRequest) cmdsurface.Meta {
			return cmdsurface.Meta{Surface: cmdsurface.SurfaceMCP, IdempotencyKey: "from-header"}
		}))
	sess := connect(t, srv.URL+"/mcp", nil)
	args := map[string]any{"name": "a"}

	plain := func() *mcp.CallToolResult {
		res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: "widget.add", Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	plain()
	if res := plain(); res.Meta[cmdsurface.MCPMetaIdempotentReplayed] != true {
		t.Fatalf("the transport's key replays: meta %v", res.Meta)
	}
	if res := keyedCall(t, sess, "widget.add", "from-meta", args); res.Meta[cmdsurface.MCPMetaIdempotentReplayed] != nil {
		t.Fatal("a _meta key is its own key, not the header's")
	}
	if run.calls.Load() != 2 {
		t.Fatalf("runs = %d, want 2", run.calls.Load())
	}
}

// TestIdempotencyReplayAsksNobody: a replay runs nothing, so the
// confirmation elicitation is not repeated for it; a declined call
// frees its key.
func TestIdempotencyReplayAsksNobody(t *testing.T) {
	run := &ordinalRunner{}
	srv, _, _ := newSurfaceHarness(t, newTestTree(), idemOpts(run), WithConfirmationElicitation(nil))

	declined := 0
	no := elicitClient(t, srv.URL+"/mcp", "decline", &declined)
	if res := keyedCall(t, no, "deploy", "d1", nil); !res.IsError {
		t.Fatal("declined call must refuse")
	}

	asked := 0
	sess := elicitClient(t, srv.URL+"/mcp", "accept", &asked)
	first := keyedCall(t, sess, "deploy", "d1", nil)
	if first.IsError || asked != 1 {
		t.Fatalf("after a decline the key is free: isError=%t text=%q asked=%d", first.IsError, textOf(first), asked)
	}
	second := keyedCall(t, sess, "deploy", "d1", nil)
	if second.Meta[cmdsurface.MCPMetaIdempotentReplayed] != true || asked != 1 {
		t.Fatalf("replay: meta %v asked %d, want replayed with no second question", second.Meta, asked)
	}
	if run.calls.Load() != 1 {
		t.Fatalf("runs = %d, want 1", run.calls.Load())
	}
}
