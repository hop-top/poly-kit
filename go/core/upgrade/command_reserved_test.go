package upgrade

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/cmdmeta"
)

// TestMigrateCommandIsReservedAndAnnotated pins that the migrate tree
// reserves itself wherever an adopter mounts it, and that every leaf
// declares its side effect: rollback restores a backup over the live
// database, so it is destructive and meets the destructive ceiling.
func TestMigrateCommandIsReservedAndAnnotated(t *testing.T) {
	cmd := MigrateCommand(NewMigrator("testapp", "1.0.0"), viper.New())
	assert.True(t, cmdmeta.InReservedTree(cmd), "migrate manages the tool's database")

	want := map[string]string{
		"status":   "read",
		"history":  "read",
		"run":      "write-local",
		"rollback": "destructive-local",
	}
	require.Len(t, cmd.Commands(), len(want))
	for _, leaf := range cmd.Commands() {
		assert.Equal(t, want[leaf.Name()], leaf.Annotations[cmdmeta.KeySideEffect], leaf.Name())
		assert.True(t, cmdmeta.InReservedTree(leaf), leaf.Name())
	}
}
