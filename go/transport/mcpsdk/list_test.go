package mcpsdk

import (
	"context"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/cmdsurface"
)

// whoHeader names the caller in these tests; the call meta treats it as
// verified.
const whoHeader = "X-Test-Who"

func toolNames(t *testing.T, sess *mcp.ClientSession) []string {
	t.Helper()
	res, err := sess.ListTools(t.Context(), nil)
	require.NoError(t, err)
	var out []string
	for _, tool := range res.Tools {
		out = append(out, tool.Name)
	}
	sort.Strings(out)
	return out
}

func TestToolListReflectsTheEstablishedCaller(t *testing.T) {
	// bob may not add widgets; nobody else is refused anything.
	perm := func(_ context.Context, meta cmdsurface.Meta, leaf *cmdsurface.Leaf) cmdsurface.PermissionDecision {
		if meta.Caller == "bob" && leaf.PathKey() == "widget add" {
			return cmdsurface.PermissionDecision{Reason: "bob may not add"}
		}
		return cmdsurface.PermissionDecision{Allowed: true}
	}
	callMeta := func(_ context.Context, req *mcp.CallToolRequest) cmdsurface.Meta {
		if req.Extra == nil || req.Extra.Header.Get(whoHeader) == "" {
			return cmdsurface.Meta{}
		}
		return cmdsurface.Meta{
			Caller:      req.Extra.Header.Get(whoHeader),
			Established: cmdsurface.EstablishedVerified,
		}
	}
	srv, _, _ := newSurfaceHarness(t, newTestTree(),
		[]cmdsurface.Option{cmdsurface.WithPermission(perm)},
		WithCallMeta(callMeta))

	all := toolNames(t, connect(t, srv.URL+"/mcp", nil))
	assert.Contains(t, all, "widget.add", "an anonymous list is the shared one")

	assert.Equal(t, all, toolNames(t, connect(t, srv.URL+"/mcp", map[string]string{whoHeader: "alice"})))

	bob := toolNames(t, connect(t, srv.URL+"/mcp", map[string]string{whoHeader: "bob"}))
	assert.NotContains(t, bob, "widget.add")
	assert.Len(t, bob, len(all)-1)
}
