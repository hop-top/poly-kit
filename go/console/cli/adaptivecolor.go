// Package cli — adaptivecolor.go provides a light/dark adaptive color that
// resolves the terminal background lazily instead of at package init.
//
// charm.land/lipgloss/v2/compat runs lipgloss.HasDarkBackground(os.Stdin,
// os.Stdout) unconditionally when the package is loaded: it puts stdin into
// raw mode, writes an OSC 11 background query plus a DA1 request to stdout,
// and reads stdin for up to 2s. That ignores NO_COLOR, leaks OSC/DA1 bytes
// into piped stdout, and steals pre-buffered stdin under a PTY. This file
// replaces the compat dependency with a local implementation that only
// queries the terminal when a color is actually resolved, and never when
// NO_COLOR is set or when stdin/stdout is not a terminal.
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
func hasDarkBackground() bool {
	bgOnce.Do(func() {
		bgDark = detectDarkBackground(
			os.Getenv("NO_COLOR"),
			term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()),
			func() bool { return lipgloss.HasDarkBackground(os.Stdin, os.Stdout) },
		)
	})
	return bgDark
}

// detectDarkBackground decides whether to treat the background as dark.
//
// Guards, in order:
//
//  1. NO_COLOR set — no styling will be emitted anyway, so never touch the
//     terminal; assume dark (upstream's fallback on any error).
//  2. stdin or stdout not a terminal — a background query cannot succeed and
//     must not write escape bytes into a pipe; assume dark.
//
// Only when both streams are real terminals is query invoked — the lipgloss
// background probe in production — preserving upstream's interactive
// behavior. query must never run in the guarded cases: it puts stdin into
// raw mode and writes escape sequences to stdout.
func detectDarkBackground(noColor string, isTerminal bool, query func() bool) bool {
	if noColor != "" {
		return true
	}
	if !isTerminal {
		return true
	}
	return query()
}
