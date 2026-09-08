// Package cli — adaptivecolor.go provides a light/dark adaptive color that
// resolves the terminal background lazily instead of at package init.
//
// charm.land/lipgloss/v2/compat runs lipgloss.HasDarkBackground(os.Stdin,
// os.Stdout) unconditionally when the package is loaded: it puts stdin into
// raw mode, writes an OSC 11 background query plus a DA1 request to stdout,
// and reads stdin for up to 2s. That ignores NO_COLOR, leaks OSC/DA1 bytes
// into piped stdout, and steals pre-buffered stdin under a PTY. This file
// replaces the compat dependency with a local implementation that only
// queries the terminal when a color is actually resolved, never when
// NO_COLOR is set or when there is no terminal, and never against
// os.Stdin — the query goes to the controlling terminal, so a piped
// stdin keeps its payload.
package cli

import (
	"image/color"
	"os"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
)

// adaptiveColor provides color options for light and dark backgrounds. The
// appropriate color is chosen when the color is resolved (RGBA), based on a
// lazily detected terminal background. Drop-in replacement for
// compat.AdaptiveColor minus the init-time terminal query.
type adaptiveColor struct {
	Light color.Color
	Dark  color.Color
}

// RGBA satisfies the color.Color interface. The background query, if any,
// runs at most once per process, on first resolution.
func (c adaptiveColor) RGBA() (r, g, b, a uint32) {
	if hasDarkBackground() {
		return c.Dark.RGBA()
	}
	return c.Light.RGBA()
}

var (
	bgOnce sync.Once
	bgDark bool
)

// hasDarkBackground reports whether the terminal background is dark,
// detecting it at most once per process.
//
// The probe reads the CONTROLLING TERMINAL, not os.Stdin, for the same
// reason the interactive prompts moved off it: os.Stdin may be a pipe
// carrying the command's payload, and a raw-mode read of it consumes
// bytes the command was meant to receive. /dev/tty is the terminal
// regardless of how stdin is redirected, so the query reaches a terminal
// that can answer it and the payload reaches the command intact.
//
// When there is no controlling terminal the probe is skipped entirely
// rather than falling back to os.Stdin: no terminal means no reply, and
// the only thing a fallback would achieve is the byte theft this avoids.
func hasDarkBackground() bool {
	bgOnce.Do(func() {
		probe := openBackgroundProbeTTY()
		if probe != nil {
			defer func() { _ = probe.Close() }()
		}
		bgDark = detectDarkBackground(
			os.Getenv("NO_COLOR"),
			probe != nil && term.IsTerminal(os.Stdout.Fd()),
			func() bool { return lipgloss.HasDarkBackground(probe, probe) },
		)
	})
	return bgDark
}

// openBackgroundProbeTTY opens the controlling terminal for the
// background query, or returns nil when there is none.
//
// A handle of its own, not the prompt helper's: that one retains a
// bufio.Reader across prompts, and a raw-mode probe reading the same fd
// would take bytes from underneath it — precisely the interleaving this
// file must not cause. Opened for the single query and closed
// immediately, because unlike a prompt it has no reason to outlive it.
//
// Overridable in tests, which have no terminal of their own.
var openBackgroundProbeTTY = func() *os.File {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil
	}
	if !term.IsTerminal(f.Fd()) {
		_ = f.Close()
		return nil
	}
	return f
}

// detectDarkBackground decides whether to treat the background as dark.
//
// Guards, in order:
//
//  1. NO_COLOR set — no styling will be emitted anyway, so never touch the
//     terminal; assume dark (upstream's fallback on any error).
//  2. no controlling terminal, or stdout not a terminal — a background query
//     cannot succeed and must not write escape bytes into a pipe; assume
//     dark.
//
// Only when there is a terminal to ask and a terminal to style is query
// invoked — the lipgloss background probe in production — preserving
// upstream's interactive behavior. query must never run in the guarded
// cases: it puts the terminal into raw mode and writes escape sequences
// to it.
func detectDarkBackground(noColor string, isTerminal bool, query func() bool) bool {
	if noColor != "" {
		return true
	}
	if !isTerminal {
		return true
	}
	return query()
}
