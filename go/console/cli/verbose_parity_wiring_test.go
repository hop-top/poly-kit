package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/contracts/parity"
)

// These tests make the -V flag wiring load-bearing: the shorthand and the
// level names in the help text must come from the parity contract, not
// from literals in the flag registration.

func verbosityData(flag string, levels map[string]string) *parity.Data {
	var d parity.Data
	d.Verbosity.Flag = flag
	d.Verbosity.Levels = levels
	return &d
}

// TestVerbosityFlagUsage_FollowsContractNotLiterals swaps both the
// shorthand and the level names away from the shipped -V / debug / trace.
// A hardcoded "V" or a hardcoded help string fails here.
func TestVerbosityFlagUsage_FollowsContractNotLiterals(t *testing.T) {
	d := verbosityData("-d", map[string]string{
		"0": "error",
		"1": "warn",
		"2": "info",
	})

	assert.Equal(t, "d", verbosityShorthand(d),
		"shorthand must follow the contract's verbosity.flag, not a hardcoded V")
	assert.Equal(t, "Increase log verbosity (-d=warn, -dd=info)", verbosityFlagUsage(d),
		"help text must be rendered from the contract's levels table")
}

// TestVerbosityFlagUsage_MatchesShippedContract pins the generated help
// text to the string the CLI shipped before the wiring, so the refactor
// cannot silently change --help output.
func TestVerbosityFlagUsage_MatchesShippedContract(t *testing.T) {
	d := &parity.Values
	require.Equal(t, "-V", d.Verbosity.Flag)

	assert.Equal(t, "V", verbosityShorthand(d))
	assert.Equal(t, "Increase log verbosity (-V=debug, -VV=trace)",
		verbosityFlagUsage(d),
		"generated help must match the historical hardcoded string")
}

// TestVerbosityFlagUsage_DegradesSafely covers contract shapes that
// cannot produce a level list.
func TestVerbosityFlagUsage_DegradesSafely(t *testing.T) {
	assert.Equal(t, "V", verbosityShorthand(verbosityData("", nil)),
		"absent flag falls back to V")
	assert.Equal(t, "V", verbosityShorthand(verbosityData("--verbose", nil)),
		"a multi-character flag is not a cobra shorthand; fall back to V")

	only0 := verbosityData("-V", map[string]string{"0": "info"})
	assert.Equal(t, "Increase log verbosity", verbosityFlagUsage(only0),
		"count 0 is the default level and is not listed")
}
