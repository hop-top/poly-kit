package mcpsdk

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hop.top/kit/go/transport/cmdsurface"
)

// recordingSink captures every audit record the bridge emits.
type recordingSink struct {
	mu   sync.Mutex
	recs []auditRec
}

type auditRec struct {
	path string
	exit int
	err  error
}

func (s *recordingSink) Emit(_ context.Context, inv cmdsurface.Invocation, res cmdsurface.Result, err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs = append(s.recs, auditRec{path: strings.Join(inv.Path, " "), exit: res.ExitCode, err: err})
	return nil
}

func (s *recordingSink) snapshot() []auditRec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]auditRec(nil), s.recs...)
}

// TestProgressStreamingRespectsPermissionGate pins that a call
// carrying a progress token meets the permission gate and the audit
// sinks exactly as a synchronous call does: streaming is a way of
// observing a call, never a way around its gates.
func TestProgressStreamingRespectsPermissionGate(t *testing.T) {
	sink := &recordingSink{}
	deny := func(_ context.Context, _ cmdsurface.Meta, leaf *cmdsurface.Leaf) cmdsurface.PermissionDecision {
		if leaf.PathKey() == "lines" {
			return cmdsurface.PermissionDecision{Reason: "lines-denied"}
		}
		return cmdsurface.PermissionDecision{Allowed: true}
	}
	srv, _, _ := newSurfaceHarness(t, linesTree(), []cmdsurface.Option{
		cmdsurface.WithPermission(deny),
		cmdsurface.WithSinks(cmdsurface.SinkSpec{Sink: sink, OnError: true, OnOK: true}),
	})
	sess := connectOpts(t, srv.URL+"/mcp", nil)

	params := &mcp.CallToolParams{Name: "lines", Arguments: map[string]any{}}
	params.SetProgressToken("tok-perm")
	res, err := sess.CallTool(t.Context(), params)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError || !strings.Contains(textOf(res), "lines-denied") {
		t.Fatalf("streamed call: isError=%t text=%q, want the permission gate's refusal", res.IsError, textOf(res))
	}
	if strings.Contains(textOf(res), "line 1") {
		t.Fatalf("streamed call ran the command past a permission refusal: %q", textOf(res))
	}

	recs := sink.snapshot()
	if len(recs) != 1 || recs[0].err == nil || !strings.Contains(recs[0].err.Error(), "permission denied") {
		t.Fatalf("audit = %+v, want one permission refusal", recs)
	}
}

// TestProgressStreamingIsAudited pins that a streamed execution
// reaches the audit sinks with its Result, as Invoke's does.
func TestProgressStreamingIsAudited(t *testing.T) {
	sink := &recordingSink{}
	srv, _, _ := newSurfaceHarness(t, linesTree(), []cmdsurface.Option{
		cmdsurface.WithSinks(cmdsurface.SinkSpec{Sink: sink, OnError: true, OnOK: true}),
	})
	sess := connectOpts(t, srv.URL+"/mcp", nil)

	params := &mcp.CallToolParams{Name: "lines", Arguments: map[string]any{}}
	params.SetProgressToken("tok-audit")
	res, err := sess.CallTool(t.Context(), params)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError = true, text: %s", textOf(res))
	}
	recs := sink.snapshot()
	if len(recs) != 1 || recs[0].path != "lines" || recs[0].err != nil || recs[0].exit != 0 {
		t.Fatalf("audit = %+v, want one clean execution of lines", recs)
	}
}
