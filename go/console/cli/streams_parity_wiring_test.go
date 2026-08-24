package cli

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/contracts/parity"
)

// These tests make the streams wiring load-bearing: they feed a
// *constructed* parity.Data (never the shared parity.json) whose values
// differ from the shipped contract, and assert the label, destination and
// flag name follow it. A port that re-hardcodes "[%s] ", os.Stderr or
// "stream" fails here.

// streamsData returns a parity.Data carrying the given streams block.
func streamsData(flag, labelFormat, output string) *parity.Data {
	var d parity.Data
	d.Streams.Flag = flag
	d.Streams.LabelFormat = labelFormat
	d.Streams.Output = output
	return &d
}

// TestStreamLabel_FollowsContractNotLiterals swaps the label template
// away from the shipped "[{name}]". A hardcoded "[%s] " fails here.
func TestStreamLabel_FollowsContractNotLiterals(t *testing.T) {
	d := streamsData("--channel", "<<{name}>>", "stdout")

	assert.Equal(t, "<<trace>> ", streamLabel(d, "trace"),
		"label must render the contract's label_format, not a hardcoded [name]")
	assert.Equal(t, os.Stdout, streamOutput(d),
		"destination must follow the contract's streams.output, not a hardcoded stderr")
	assert.Equal(t, "channel", streamFlagName(d),
		"flag name must follow the contract's streams.flag, not a hardcoded \"stream\"")
}

// TestStreamLabel_MatchesShippedContract pins the wiring to the values
// actually declared in parity.json, so the refactor cannot silently
// change observable behavior.
func TestStreamLabel_MatchesShippedContract(t *testing.T) {
	d := &parity.Values
	require.Equal(t, "[{name}]", d.Streams.LabelFormat)
	require.Equal(t, "stderr", d.Streams.Output)
	require.Equal(t, "--stream", d.Streams.Flag)

	assert.Equal(t, "[trace] ", streamLabel(d, "trace"),
		"shipped contract must still render the historical [name] prefix")
	assert.Equal(t, os.Stderr, streamOutput(d))
	assert.Equal(t, "stream", streamFlagName(d))
}

// TestStreamLabel_DegradesSafely covers contract shapes the loader could
// legitimately encounter without producing a bare or panicking label.
func TestStreamLabel_DegradesSafely(t *testing.T) {
	empty := streamsData("", "", "")
	assert.Equal(t, "[trace] ", streamLabel(empty, "trace"),
		"absent label_format falls back to the [name] default")
	assert.Equal(t, os.Stderr, streamOutput(empty),
		"absent output falls back to stderr")
	assert.Equal(t, "stream", streamFlagName(empty),
		"absent flag falls back to \"stream\"")

	noPlaceholder := streamsData("--s", "LOG:", "stderr")
	assert.Equal(t, "LOG: ", streamLabel(noPlaceholder, "trace"),
		"a label_format without {name} renders verbatim")
	assert.Equal(t, "s", streamFlagName(noPlaceholder))
}
