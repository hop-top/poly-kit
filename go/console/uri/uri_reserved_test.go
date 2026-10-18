package uri

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"

	"hop.top/kit/go/console/cli/cmdmeta"
)

// TestURICommandIsReservedWhereverItIsMounted pins that the uri tree
// reserves itself, so mounting Command directly withholds it from
// served surfaces exactly as cli.WithURI does — handler generate
// writes the desktop integration of the tool — and that every leaf
// declares its side effect.
func TestURICommandIsReservedWhereverItIsMounted(t *testing.T) {
	cmd := Command(Config{Handler: HandlerConfig{}})
	assert.True(t, cmdmeta.InReservedTree(cmd))
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Runnable() && !c.HasSubCommands() {
			assert.NotEmpty(t, c.Annotations[cmdmeta.KeySideEffect], c.CommandPath())
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(cmd)
}
