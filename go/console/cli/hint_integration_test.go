package cli_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

func TestIntegration_HintsFieldPopulated(t *testing.T) {
	r := cli.New(cli.Config{Name: "t", Version: "0.0.1", Short: "test", DisableValidate: true})
	assert.NotNil(t, r.Hints, "Root.Hints must be initialized")
}

func TestIntegration_NoHintsFlagDisables(t *testing.T) {
	r := cli.New(cli.Config{Name: "t", Version: "0.0.1", Short: "test", DisableValidate: true})
	require.NoError(t, r.Cmd.PersistentFlags().Set("no-hints", "true"))
	assert.False(t, output.HintsEnabled(r.Viper),
		"HintsEnabled must return false when --no-hints is set")
}

func TestIntegration_HintsEnabledByDefault(t *testing.T) {
	r := cli.New(cli.Config{Name: "t", Version: "0.0.1", Short: "test", DisableValidate: true})
	assert.True(t, output.HintsEnabled(r.Viper),
		"HintsEnabled must return true by default")
}

func TestIntegration_QuietSuppressesHints(t *testing.T) {
	r := cli.New(cli.Config{Name: "t", Version: "0.0.1", Short: "test", DisableValidate: true})
	require.NoError(t, r.Cmd.PersistentFlags().Set("quiet", "true"))
	assert.False(t, output.HintsEnabled(r.Viper),
		"HintsEnabled must return false when --quiet is set")
}

func TestIntegration_EnvVarSuppressesHints(t *testing.T) {
	t.Setenv("HOP_QUIET_HINTS", "1")
	r := cli.New(cli.Config{Name: "t", Version: "0.0.1", Short: "test", DisableValidate: true})
	assert.False(t, output.HintsEnabled(r.Viper),
		"HintsEnabled must return false when HOP_QUIET_HINTS=1")
}

func TestIntegration_UpgradeHintRegisters(t *testing.T) {
	r := cli.New(cli.Config{Name: "hop", Version: "0.0.1", Short: "test", DisableValidate: true})
	upgraded := false
	output.RegisterUpgradeHints(r.Hints, "hop", &upgraded)

	hints := r.Hints.Lookup("upgrade")
	require.Len(t, hints, 1)
	assert.Contains(t, hints[0].Message, "hop version")

	// Condition false => not active.
	active := output.Active(hints)
	assert.Empty(t, active)

	// After upgrade => active.
	upgraded = true
	active = output.Active(hints)
	require.Len(t, active, 1)
}

func TestIntegration_VersionHintRegisters(t *testing.T) {
	r := cli.New(cli.Config{Name: "hop", Version: "0.0.1", Short: "test", DisableValidate: true})
	updateAvail := false
	output.RegisterVersionHints(r.Hints, "hop", &updateAvail)

	hints := r.Hints.Lookup("version")
	require.Len(t, hints, 1)
	assert.Contains(t, hints[0].Message, "hop upgrade")

	active := output.Active(hints)
	assert.Empty(t, active)

	updateAvail = true
	active = output.Active(hints)
	require.Len(t, active, 1)
}
