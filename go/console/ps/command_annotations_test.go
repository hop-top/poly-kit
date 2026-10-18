package ps_test

import (
	"context"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"

	"hop.top/kit/go/console/cli/cmdmeta"
	"hop.top/kit/go/console/ps"
)

type noProcs struct{}

func (noProcs) List(context.Context) ([]ps.Entry, error) { return nil, nil }

// TestPsCommandIsReadOnlyAndServable pins the ps decision: it lists
// work, changes nothing, declares read, and stays servable.
func TestPsCommandIsReadOnlyAndServable(t *testing.T) {
	cmd := ps.Command("tool", noProcs{}, viper.New())
	assert.Equal(t, "read", cmd.Annotations[cmdmeta.KeySideEffect])
	assert.False(t, cmdmeta.InReservedTree(cmd))
}
