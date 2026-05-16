package cli_test

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
)

func TestVerboseCount_Default(t *testing.T) {
	r := cli.New(cli.Config{
		Name: "vtool", Version: "0.1.0", Short: "verbose test",
		DisableValidate: true,
	})
	assert.Equal(t, 0, r.VerboseCount(), "default verbose count must be 0")
}

func TestVerboseCount_SingleV(t *testing.T) {
	r := cli.New(cli.Config{
		Name: "vtool", Version: "0.1.0", Short: "verbose test",
		DisableValidate: true,
	})
	r.Cmd.RunE = func(_ *cobra.Command, _ []string) error { return nil }
	r.SetArgs([]string{"-V"})
	err := r.Execute(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, r.VerboseCount())
}

func TestVerboseCount_StackedVV(t *testing.T) {
	r := cli.New(cli.Config{
		Name: "vtool", Version: "0.1.0", Short: "verbose test",
		DisableValidate: true,
	})
	r.Cmd.RunE = func(_ *cobra.Command, _ []string) error { return nil }
	r.SetArgs([]string{"-VV"})
	err := r.Execute(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 2, r.VerboseCount())
}

func TestVerboseCount_LongFlag(t *testing.T) {
	r := cli.New(cli.Config{
		Name: "vtool", Version: "0.1.0", Short: "verbose test",
		DisableValidate: true,
	})
	r.Cmd.RunE = func(_ *cobra.Command, _ []string) error { return nil }
	r.SetArgs([]string{"--verbose", "--verbose"})
	err := r.Execute(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 2, r.VerboseCount())
}

func TestVerboseFlag_InHelp(t *testing.T) {
	r := cli.New(cli.Config{
		Name: "vtool", Version: "0.1.0", Short: "verbose test",
		DisableValidate: true,
	})
	f := r.Cmd.PersistentFlags().Lookup("verbose")
	require.NotNil(t, f, "--verbose flag must be registered")
	assert.Equal(t, "V", f.Shorthand, "shorthand must be -V")
}
