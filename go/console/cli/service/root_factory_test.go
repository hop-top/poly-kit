package service

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
)

func TestOperatorGlobalsRoundTrip(t *testing.T) {
	// The serving root parsed -c twice, --region once, and left
	// --verbose alone; a fresh tree must see exactly that, Changed
	// bits included, so viper reports the values as parsed.
	cfg := cli.Config{
		Name: "p", Version: "0.1.0", DisableValidate: true,
		Globals: []cli.Flag{{Name: "region", Default: "us", Usage: "region"}},
	}
	served := cli.New(cfg)
	served.Cmd.AddCommand(&cobra.Command{
		Use:  "noop",
		RunE: func(*cobra.Command, []string) error { return nil },
	})
	served.SetArgs([]string{"-c", "a=1", "-c", "b=2", "--region", "eu", "noop"})
	require.NoError(t, served.Execute(context.Background()))

	fresh := cli.New(cfg)
	replayOperatorGlobals(fresh.Cmd, captureOperatorGlobals(served.Cmd))

	assert.Equal(t, "eu", fresh.Viper.GetString("region"))
	assert.Equal(t, map[string]any{"a": 1, "b": 2}, fresh.ConfigOverrides())
	assert.Equal(t, 0, fresh.VerboseCount(), "an untouched flag stays at its default")
	assert.True(t, fresh.Cmd.PersistentFlags().Lookup("region").Changed)
}
