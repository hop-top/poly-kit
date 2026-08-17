package log_test

import (
	"testing"

	charmlog "charm.land/log/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/contracts/parity"
	kitlog "hop.top/kit/go/console/log"
)

// These tests make the verbosity wiring load-bearing: they feed a
// *constructed* parity.Data (never the shared parity.json) whose values
// differ from the shipped contract, and assert the level mapping follows
// it. A port that re-hardcodes info/debug/trace/warn fails here.

// contractData returns a parity.Data carrying the given verbosity block.
func contractData(levels map[string]string, quiet string) *parity.Data {
	var d parity.Data
	d.Verbosity.Levels = levels
	d.Verbosity.QuietOverride = quiet
	return &d
}

// TestVerbosityLevel_FollowsContractNotLiterals swaps the level names
// away from the shipped info/debug/trace. If WithVerbose's mapping is
// hardcoded, the resolved levels stay at the shipped values and this
// fails.
func TestVerbosityLevel_FollowsContractNotLiterals(t *testing.T) {
	d := contractData(map[string]string{
		"0": "error",
		"1": "warn",
		"2": "info",
	}, "fatal")

	assert.Equal(t, charmlog.ErrorLevel, kitlog.VerbosityLevel(d, 0),
		"count 0 must resolve via contract levels[\"0\"], not a hardcoded Info")
	assert.Equal(t, charmlog.WarnLevel, kitlog.VerbosityLevel(d, 1),
		"count 1 must resolve via contract levels[\"1\"], not a hardcoded Debug")
	assert.Equal(t, charmlog.InfoLevel, kitlog.VerbosityLevel(d, 2),
		"count 2 must resolve via contract levels[\"2\"], not a hardcoded Trace")
	assert.Equal(t, charmlog.InfoLevel, kitlog.VerbosityLevel(d, 7),
		"counts above the highest declared key clamp to that key's level")

	assert.Equal(t, charmlog.FatalLevel, kitlog.QuietLevel(d),
		"quiet must resolve via contract quiet_override, not a hardcoded Warn")
}

// TestVerbosityLevel_ExtraContractLevel proves the mapping is table-driven
// rather than a three-branch switch: a contract declaring a 4th level must
// be honored.
func TestVerbosityLevel_ExtraContractLevel(t *testing.T) {
	d := contractData(map[string]string{
		"0": "warn",
		"3": "trace",
	}, "error")

	assert.Equal(t, charmlog.WarnLevel, kitlog.VerbosityLevel(d, 0))
	assert.Equal(t, charmlog.WarnLevel, kitlog.VerbosityLevel(d, 2),
		"counts between declared keys fall back to the nearest lower key")
	assert.Equal(t, kitlog.TraceLevel, kitlog.VerbosityLevel(d, 3),
		"a contract key beyond the shipped 0/1/2 must still be honored")
	assert.Equal(t, charmlog.ErrorLevel, kitlog.QuietLevel(d))
}

// TestVerbosityLevel_MatchesShippedContract pins the wiring to the values
// actually declared in parity.json, so the refactor cannot silently
// change observable behavior.
func TestVerbosityLevel_MatchesShippedContract(t *testing.T) {
	d := &parity.Values
	require.NotEmpty(t, d.Verbosity.Levels, "parity.json verbosity.levels must be loaded")

	assert.Equal(t, "info", d.Verbosity.Levels["0"])
	assert.Equal(t, "debug", d.Verbosity.Levels["1"])
	assert.Equal(t, "trace", d.Verbosity.Levels["2"])
	assert.Equal(t, "warn", d.Verbosity.QuietOverride)

	assert.Equal(t, charmlog.InfoLevel, kitlog.VerbosityLevel(d, 0))
	assert.Equal(t, charmlog.DebugLevel, kitlog.VerbosityLevel(d, 1))
	assert.Equal(t, kitlog.TraceLevel, kitlog.VerbosityLevel(d, 2))
	assert.Equal(t, kitlog.TraceLevel, kitlog.VerbosityLevel(d, 3))
	assert.Equal(t, charmlog.WarnLevel, kitlog.QuietLevel(d))
}

// TestVerbosityLevel_DegradesSafely covers contract shapes the loader
// could legitimately encounter without panicking.
func TestVerbosityLevel_DegradesSafely(t *testing.T) {
	empty := contractData(nil, "")
	assert.Equal(t, charmlog.InfoLevel, kitlog.VerbosityLevel(empty, 0))
	assert.Equal(t, charmlog.WarnLevel, kitlog.QuietLevel(empty),
		"absent quiet_override falls back to Warn")

	bogus := contractData(map[string]string{"0": "nonsense", "x": "debug"}, "nonsense")
	assert.Equal(t, charmlog.InfoLevel, kitlog.VerbosityLevel(bogus, 0),
		"unrecognized level name falls back to Info")
	assert.Equal(t, charmlog.WarnLevel, kitlog.QuietLevel(bogus))
}
