package stage_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/cmdmeta"
	stagecmd "hop.top/kit/go/console/stage"
)

// TestStageCommandIsReservedAndAnnotated pins that the stage tree
// reserves itself wherever an adopter mounts it — set changes what a
// scope may do, for everyone on it — and that every leaf declares its
// side effect.
func TestStageCommandIsReservedAndAnnotated(t *testing.T) {
	cmd := stagecmd.New(stagecmd.Config{})
	assert.True(t, cmdmeta.InReservedTree(cmd))

	want := map[string]string{"show": "read", "why": "read", "list": "read", "set": "write"}
	require.Len(t, cmd.Commands(), len(want))
	for _, leaf := range cmd.Commands() {
		assert.Equal(t, want[leaf.Name()], leaf.Annotations[cmdmeta.KeySideEffect], leaf.Name())
	}
}
