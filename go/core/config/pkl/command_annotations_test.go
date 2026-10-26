package pkl

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"hop.top/kit/go/console/cli/cmdmeta"
)

// TestConfigInitIsReserved pins that the config wizard reserves
// itself, which withholds it from every served surface, and keeps its
// write-local tier, which keeps --dry-run on the command line.
func TestConfigInitIsReserved(t *testing.T) {
	cmd := NewConfigCommand("config.pkl", CommandOpts{})
	assert.True(t, cmdmeta.InReservedTree(cmd), "init writes the tool's configuration")
	assert.Equal(t, "write-local", cmd.Annotations[cmdmeta.KeySideEffect])
}
