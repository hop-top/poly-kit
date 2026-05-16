package output_test

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/output"
)

func TestRegisterFlagsWith_Defaults(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	v := viper.New()
	output.RegisterFlagsWith(cmd, v)

	pf := cmd.PersistentFlags()
	require.NotNil(t, pf.Lookup("format"))
	require.NotNil(t, pf.Lookup("format-opt"))
	require.NotNil(t, pf.Lookup("format-help"))
	require.NotNil(t, pf.Lookup("cols"))
	require.NotNil(t, pf.Lookup("columns"))
	require.NotNil(t, pf.Lookup("template"))
	require.NotNil(t, pf.Lookup("output"))
	assert.Equal(t, "o", pf.Lookup("output").Shorthand)
}

func TestRegisterFlagsWith_DisableOutput(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	v := viper.New()
	output.RegisterFlagsWith(cmd, v, output.DisableOutputFlag())
	assert.Nil(t, cmd.PersistentFlags().Lookup("output"))
	assert.NotNil(t, cmd.PersistentFlags().Lookup("format"))
}

func TestRegisterFlagsWith_StringSliceFormatOpt(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	v := viper.New()
	output.RegisterFlagsWith(cmd, v)

	pf := cmd.PersistentFlags().Lookup("format-opt")
	require.NoError(t, pf.Value.Set("a=1"))
	require.NoError(t, pf.Value.Set("b=2"))
	got := v.GetStringSlice("format-opt")
	assert.Equal(t, []string{"a=1", "b=2"}, got)
}

func TestRegisterFlagsWith_ColsCommaSplitInDispatch(t *testing.T) {
	// cobra's StringSlice splits on commas natively when set via flag
	// parsing; verify our flag wiring exposes that.
	cmd := &cobra.Command{Use: "x"}
	v := viper.New()
	output.RegisterFlagsWith(cmd, v)

	pf := cmd.PersistentFlags().Lookup("cols")
	require.NoError(t, pf.Value.Set("Name,Score"))
	got := v.GetStringSlice("cols")
	assert.ElementsMatch(t, []string{"Name", "Score"}, got)
}

func TestRegisterFlagsWith_OutputBindsToViper(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	v := viper.New()
	output.RegisterFlagsWith(cmd, v)
	require.NoError(t, cmd.PersistentFlags().Set("output", "/tmp/x.json"))
	assert.Equal(t, "/tmp/x.json", v.GetString("output"))
}
