package cli_test

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/contracts/parity"
	"hop.top/kit/go/console/cli"
	kitlog "hop.top/kit/go/console/log"
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

func newQuietRoot(t *testing.T, d cli.Disable) *cli.Root {
	t.Helper()
	r := cli.New(cli.Config{
		Name: "qtool", Version: "0.1.0", Short: "quiet test",
		Disable: d, DisableValidate: true,
	})
	r.Cmd.AddCommand(&cobra.Command{
		Use:  "sub",
		RunE: func(*cobra.Command, []string) error { return nil },
	})
	return r
}

func TestIsQuiet_NilRoot(t *testing.T) {
	var r *cli.Root
	assert.False(t, r.IsQuiet(), "nil root must report not quiet")
}

func TestIsQuiet_Default(t *testing.T) {
	r := newQuietRoot(t, cli.Disable{})
	assert.False(t, r.IsQuiet(), "unparsed: the default")

	r.SetArgs([]string{"sub"})
	require.NoError(t, r.Execute(t.Context()))
	assert.False(t, r.IsQuiet(), "parsed without --quiet")
}

func TestIsQuiet_ParsedBySubcommand(t *testing.T) {
	r := newQuietRoot(t, cli.Disable{})
	r.SetArgs([]string{"sub", "--quiet"})
	require.NoError(t, r.Execute(t.Context()))
	assert.True(t, r.IsQuiet())
}

// TestIsQuiet_ReadsViper: quiet resolved from config or env lands in the
// viper key the logger reads, not on the flag; the accessor must see it.
func TestIsQuiet_ReadsViper(t *testing.T) {
	r := newQuietRoot(t, cli.Disable{})
	r.Viper.Set("quiet", true)
	assert.True(t, r.IsQuiet())
}

func TestIsQuiet_FlagDisabled(t *testing.T) {
	r := newQuietRoot(t, cli.Disable{Quiet: true})
	r.SetArgs([]string{"sub"})
	require.NoError(t, r.Execute(t.Context()))
	assert.False(t, r.IsQuiet())
}

// TestIsQuiet_WithVerbose: --quiet and -V are not mutually exclusive.
// VerboseCount keeps the raw count; quiet wins at the logger, which
// floors the level at the contract's quiet_override.
func TestIsQuiet_WithVerbose(t *testing.T) {
	r := newQuietRoot(t, cli.Disable{})
	r.SetArgs([]string{"-VV", "--quiet", "sub"})
	require.NoError(t, r.Execute(t.Context()))
	assert.True(t, r.IsQuiet())
	assert.Equal(t, 2, r.VerboseCount())

	l := kitlog.WithVerbose(r.Viper, r.VerboseCount())
	assert.Equal(t, kitlog.QuietLevel(&parity.Values), l.GetLevel(),
		"IsQuiet must agree with the level the logger acts on")
}
