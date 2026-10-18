package router_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/cmdmeta"
	"hop.top/kit/go/console/cli/router"
)

// TestRouterCommandIsReservedAndAnnotated pins that the router tree,
// which starts and kills server processes on the host, reserves itself
// wherever it is mounted, and that every leaf declares its side
// effect: start starts a server, so it is also self-hosting.
func TestRouterCommandIsReservedAndAnnotated(t *testing.T) {
	cmd := router.Cmd()
	assert.True(t, cmdmeta.InReservedTree(cmd))

	want := map[string]string{"start": "write-local", "stop": "destructive-local", "list": "read", "config": "read"}
	require.Len(t, cmd.Commands(), len(want))
	for _, leaf := range cmd.Commands() {
		assert.Equal(t, want[leaf.Name()], leaf.Annotations[cmdmeta.KeySideEffect], leaf.Name())
		assert.Equal(t, leaf.Name() == "start", cmdmeta.IsSelfHosting(leaf), leaf.Name())
	}
}
