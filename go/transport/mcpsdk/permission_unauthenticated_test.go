package mcpsdk

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hop.top/kit/go/transport/cmdsurface"
)

// A permission refusal that says it is an authentication failure
// reaches the MCP client as an isError result that names the
// authentication class and the host's reason; an ordinary refusal is
// the permission class. Both are audited with their class.
func TestPermissionUnauthenticatedIsAnAuthenticationRefusal(t *testing.T) {
	sink := &recordingSink{}
	var caller string
	perm := func(_ context.Context, meta cmdsurface.Meta, _ *cmdsurface.Leaf) cmdsurface.PermissionDecision {
		switch meta.Caller {
		case "ghost":
			return cmdsurface.PermissionDecision{Reason: "unknown_principal", Unauthenticated: true}
		case "mallory":
			return cmdsurface.PermissionDecision{Reason: "no_grant"}
		}
		return cmdsurface.PermissionDecision{Allowed: true}
	}
	srv, _, _ := newSurfaceHarness(t, newTestTree(),
		[]cmdsurface.Option{
			cmdsurface.WithPermission(perm),
			cmdsurface.WithSinks(cmdsurface.SinkSpec{Sink: sink, OnError: true, OnOK: true}),
		},
		WithCallMeta(func(context.Context, *mcp.CallToolRequest) cmdsurface.Meta {
			return cmdsurface.Meta{Caller: caller, Established: cmdsurface.EstablishedVerified}
		}))
	sess := connect(t, srv.URL+"/mcp", nil)

	caller = "ghost"
	text, isErr := callText(t, sess, "ping")
	if !isErr || !strings.Contains(text, "authentication refused") || !strings.Contains(text, "unknown_principal") {
		t.Fatalf("ghost: isError=%t text=%q, want an authentication refusal with the reason", isErr, text)
	}
	caller = "mallory"
	text, isErr = callText(t, sess, "ping")
	if !isErr || !strings.Contains(text, "permission denied") || strings.Contains(text, "authentication") {
		t.Fatalf("mallory: isError=%t text=%q, want the permission refusal", isErr, text)
	}

	recs := sink.snapshot()
	if len(recs) != 2 {
		t.Fatalf("audit = %+v, want two records", recs)
	}
	if !errors.Is(recs[0].err, cmdsurface.ErrAuthRefused) || errors.Is(recs[0].err, cmdsurface.ErrPermissionDenied) ||
		!strings.Contains(recs[0].err.Error(), "unknown_principal") {
		t.Errorf("record 0 = %v, want the authentication class and the reason", recs[0].err)
	}
	if !errors.Is(recs[1].err, cmdsurface.ErrPermissionDenied) || errors.Is(recs[1].err, cmdsurface.ErrAuthRefused) {
		t.Errorf("record 1 = %v, want the permission class", recs[1].err)
	}
}
