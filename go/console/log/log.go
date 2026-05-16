// Package log provides a thin wrapper around charm.land/log/v2 that
// reads configuration from a viper instance.
//
// The viper keys "quiet" and "no-color" (bound by kit/cli) control
// behavior:
//
//   - quiet  => WarnLevel (suppresses info and below)
//   - no-color => disables ANSI color output
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

	"charm.land/lipgloss/v2"
	"charm.land/log/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/exp/charmtone"
	"github.com/spf13/viper"
)

// TraceLevel is one step below DebugLevel for high-volume diagnostics.
// charm/log doesn't define Trace, so we slot it in below Debug.
const TraceLevel log.Level = log.DebugLevel - 1

// New returns a *log.Logger at InfoLevel, respecting the "quiet" and
// "no-color" viper keys. When quiet is true the level is raised to
// WarnLevel.
func New(v *viper.Viper) *log.Logger {
	return WithLevel(v, log.InfoLevel)
}

// WithVerbose returns a logger at the level implied by verbose count.
// Count 0=Info, 1=Debug, 2+=Trace. Quiet (from viper) overrides to Warn.
func WithVerbose(v *viper.Viper, verbose int) *log.Logger {
	level := log.InfoLevel
	switch {
	case verbose >= 2:
		level = TraceLevel
	case verbose == 1:
		level = log.DebugLevel
	}
	return WithLevel(v, level)
}

// WithLevel returns a *log.Logger at the given level, still respecting
// "quiet" (which overrides to WarnLevel when set) and "no-color".
func WithLevel(v *viper.Viper, level log.Level) *log.Logger {
	if v.GetBool("quiet") && level < log.WarnLevel {
		level = log.WarnLevel
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
