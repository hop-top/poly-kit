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
// the contract's quiet_override wins when --quiet is set (see IsQuiet).
//
// The count is read from the persistent flag at call time: cobra
// shares one *pflag.Flag between the root's persistent set and every
// subcommand's merged set, so whichever command parsed -V, the value
// is here. Before parsing it is the default, 0.
func (r *Root) VerboseCount() int {
	if r == nil || r.Cmd == nil {
		return 0
	}
	n, err := r.Cmd.PersistentFlags().GetCount("verbose")
	if err != nil {
		return 0
	}
	return n
}

// IsQuiet reports whether --quiet is in effect.
//
// It reads the "quiet" key from r.Viper, the same key kit/log reads, so
// it agrees with the logger: a quiet set through config or env counts,
// not only the flag. --quiet and -V are not mutually exclusive;
// VerboseCount keeps the raw count and quiet wins at the logger, which
// floors the level at the contract's quiet_override. Before parsing, or
// with Disable.Quiet and no other source, it is false.
func (r *Root) IsQuiet() bool {
	if r == nil || r.Viper == nil {
		return false
	}
	return r.Viper.GetBool("quiet")
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
