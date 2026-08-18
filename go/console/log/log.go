// Package log provides a thin wrapper around charm.land/log/v2 that
// reads configuration from a viper instance.
//
// The viper keys "quiet" and "no-color" (bound by kit/cli) control
// behavior:
//
//   - quiet  => the parity contract's verbosity.quiet_override level
//   - no-color => disables ANSI color output
//
// The -V count → level mapping and the quiet override both come from
// contracts/parity (parity.json verbosity block), so the level vocabulary
// stays identical across the Go, TypeScript and Python ports.
//
// Level prefixes are styled with the hop.top theme palette:
//
//   - error = Cherry (red)
//   - warn  = Yam (amber)
//   - info  = Squid (muted)
//   - debug = Smoke (dim)
//
// Output is always directed to os.Stderr.
package log

import (
	"os"
	"strconv"

	"charm.land/lipgloss/v2"
	"charm.land/log/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/spf13/viper"

	"hop.top/kit/contracts/parity"
)

// TraceLevel is one step below DebugLevel for high-volume diagnostics.
// charm/log doesn't define Trace, so we slot it in below Debug.
const TraceLevel log.Level = log.DebugLevel - 1

// levelByName resolves a contract level name to a charm/log level.
// The names are the cross-language vocabulary declared in parity.json;
// TraceLevel is kit-local because charm/log has no Trace.
func levelByName(name string) (log.Level, bool) {
	switch name {
	case "trace":
		return TraceLevel, true
	case "debug":
		return log.DebugLevel, true
	case "info":
		return log.InfoLevel, true
	case "warn":
		return log.WarnLevel, true
	case "error":
		return log.ErrorLevel, true
	case "fatal":
		return log.FatalLevel, true
	}
	return 0, false
}

// VerbosityLevel resolves a -V count against a parity contract's
// verbosity.levels table. Counts above the highest declared key clamp to
// that key's level; an empty or unresolvable table falls back to Info.
//
// Taking the contract as a parameter (rather than reading parity.Values)
// keeps the mapping testable against a constructed Data without mutating
// the shared parity.json.
func VerbosityLevel(d *parity.Data, verbose int) log.Level {
	levels := d.Verbosity.Levels
	if len(levels) == 0 {
		return log.InfoLevel
	}
	// Highest declared count wins for anything at or above it.
	best := -1
	for k := range levels {
		n, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		if n <= verbose && n > best {
			best = n
		}
	}
	if best < 0 {
		return log.InfoLevel
	}
	lvl, ok := levelByName(levels[strconv.Itoa(best)])
	if !ok {
		return log.InfoLevel
	}
	return lvl
}

// QuietLevel resolves a parity contract's verbosity.quiet_override to a
// charm/log level. An unrecognized or absent override falls back to Warn.
func QuietLevel(d *parity.Data) log.Level {
	lvl, ok := levelByName(d.Verbosity.QuietOverride)
	if !ok {
		return log.WarnLevel
	}
	return lvl
}

// New returns a *log.Logger at the contract's zero-verbosity level,
// respecting the "quiet" and "no-color" viper keys. When quiet is true
// the level is raised to the contract's quiet_override level.
func New(v *viper.Viper) *log.Logger {
	return WithLevel(v, VerbosityLevel(&parity.Values, 0))
}

// WithVerbose returns a logger at the level implied by verbose count,
// resolved through the parity contract's verbosity.levels table. Quiet
// (from viper) overrides to the contract's quiet_override level.
func WithVerbose(v *viper.Viper, verbose int) *log.Logger {
	return WithLevel(v, VerbosityLevel(&parity.Values, verbose))
}

// WithLevel returns a *log.Logger at the given level, still respecting
// "quiet" (which overrides to the contract's quiet_override level when
// set) and "no-color".
func WithLevel(v *viper.Viper, level log.Level) *log.Logger {
	if quiet := QuietLevel(&parity.Values); v.GetBool("quiet") && level < quiet {
		level = quiet
	}

	l := log.NewWithOptions(os.Stderr, log.Options{
		Level: level,
	})

	if v.GetBool("no-color") {
		l.SetColorProfile(colorprofile.NoTTY)
	}

	l.SetStyles(styles())
	return l
}

// styles returns hop.top-themed level prefix styles.
func styles() *log.Styles {
	s := log.DefaultStyles()
	s.Levels[log.ErrorLevel] = lipgloss.NewStyle().
		SetString("ERRO").
		Bold(true).
		Foreground(charmtone.Cherry)
	s.Levels[log.WarnLevel] = lipgloss.NewStyle().
		SetString("WARN").
		Bold(true).
		Foreground(charmtone.Yam)
	s.Levels[log.InfoLevel] = lipgloss.NewStyle().
		SetString("INFO").
		Foreground(charmtone.Squid)
	s.Levels[log.DebugLevel] = lipgloss.NewStyle().
		SetString("DEBU").
		Foreground(charmtone.Smoke)
	s.Levels[TraceLevel] = lipgloss.NewStyle().
		SetString("TRAC").
		Foreground(charmtone.Smoke)
	s.Levels[log.FatalLevel] = lipgloss.NewStyle().
		SetString("FATA").
		Bold(true).
		Foreground(charmtone.Cherry)
	return s
}
