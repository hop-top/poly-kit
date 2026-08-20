package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"hop.top/kit/contracts/parity"
)

// VerboseCount returns the -V count from the root command.
// Counts map to levels via the parity contract's verbosity.levels table;
// the contract's quiet_override wins when --quiet is set.
func (r *Root) VerboseCount() int {
	return r.verboseCount
}

// verbosityShorthand returns the single-character shorthand declared by
// the parity contract's verbosity.flag, with the leading dash stripped
// (cobra registers shorthands undashed). A contract value that is not a
// single character after stripping falls back to "V".
func verbosityShorthand(d *parity.Data) string {
	s := strings.TrimLeft(d.Verbosity.Flag, "-")
	if len(s) != 1 {
		return "V"
	}
	return s
}

// verbosityFlagUsage renders the -V help text from the contract's
// verbosity.levels table, e.g. "Increase log verbosity (-V=debug,
// -VV=trace)". Count 0 is the default level and is not listed.
func verbosityFlagUsage(d *parity.Data) string {
	short := verbosityShorthand(d)

	counts := make([]int, 0, len(d.Verbosity.Levels))
	for k := range d.Verbosity.Levels {
		n, err := strconv.Atoi(k)
		if err != nil || n < 1 {
			continue
		}
		counts = append(counts, n)
	}
	sort.Ints(counts)

	parts := make([]string, 0, len(counts))
	for _, n := range counts {
		parts = append(parts, fmt.Sprintf("-%s=%s",
			strings.Repeat(short, n), d.Verbosity.Levels[strconv.Itoa(n)]))
	}
	if len(parts) == 0 {
		return "Increase log verbosity"
	}
	return "Increase log verbosity (" + strings.Join(parts, ", ") + ")"
}
