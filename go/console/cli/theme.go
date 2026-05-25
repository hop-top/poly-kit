// Package cli — theme.go defines the Theme struct and builder for kit CLIs.
//
// Three named palettes ship with kit:
//
//   - Neon    — vivid: grass green (#7ED957), neon pink (#FF00FF)
//   - Dark    — softer: lime (#C1FF72), pink (#FF66C4)
//   - Bauhaus — high-contrast primaries: magenta (#FF00AA), blue (#0000FF)
//
// CharmTone is used only for muted, success, and warn (per palette). Error is
// bold magenta across all palettes for a consistent, high-impact signal.
//
// Title renders as an inverse chip: white-on-black on light terminals,
// black-on-white on dark. This makes titles act as labels/banners rather than
// blending with body text. Never hardcode a single Title foreground — it will
// disappear in the inverse mode.
package cli

import (
	"image/color"

	"charm.land/lipgloss/v2"
	// charm.land/lipgloss/v2/compat runs an unconditional background-color
	// query at package init that ignores NO_COLOR, leaking OSC/DA1 bytes to
	// stdout and stealing stdin under a PTY. Upstream fix proposed in
	// https://github.com/charmbracelet/lipgloss/pull/692; once merged and
	// released, bump charm.land/lipgloss/v2 to pick it up and this note can
	// be removed. Until then, callers that need clean stdout / non-leaky
	// stdin should set NO_COLOR before importing this package transitively.
	"charm.land/lipgloss/v2/compat"
	"github.com/charmbracelet/x/exp/charmtone"
)

// Palette holds the two brand colors used across a theme.
type Palette struct {
	Command color.Color // commands / primary accent
	Flag    color.Color // flags / secondary accent
}

// Built-in palettes.
var (
	Neon = Palette{
		Command: lipgloss.Color("#7ED957"), // grass green
		Flag:    lipgloss.Color("#FF00FF"), // vivid neon pink
	}
	Dark = Palette{
		Command: lipgloss.Color("#C1FF72"), // lime
		Flag:    lipgloss.Color("#FF66C4"), // pink
	}
	Bauhaus = Palette{
		Command: lipgloss.Color("#FF00AA"), // magenta
		Flag:    lipgloss.Color("#0000FF"), // blue
	}
)

// Theme holds semantic colors and pre-built lipgloss styles for CLI output.
type Theme struct {
	// Brand colors.
	Palette Palette

	// Semantic colors.
	Accent    color.Color
	Secondary color.Color
	Muted     color.Color
	Error     color.Color
	Success   color.Color
	Warn      color.Color

	// Pre-built styles.
	Title  lipgloss.Style
	Subtle lipgloss.Style
	Bold   lipgloss.Style
}

// buildTheme constructs a Theme. When accent is non-empty it is used as the
// command color; otherwise the Neon palette is used.
func buildTheme(accent string) Theme {
	p := Neon
	if accent != "" {
		p.Command = lipgloss.Color(accent)
	}
	return themeFromPalette(p)
}

// themeFromPalette builds a full Theme from a Palette.
func themeFromPalette(p Palette) Theme {
	muted := color.Color(charmtone.Squid)
	// Title renders as an inverse chip: white-on-black on light terminals,
	// black-on-white on dark. The compat AdaptiveColor reads its decision from
	// a package-level background probe done once at init.
	titleFg := color.Color(compat.AdaptiveColor{
		Light: lipgloss.Color("#FFFFFF"),
		Dark:  lipgloss.Color("#000000"),
	})
	titleBg := color.Color(compat.AdaptiveColor{
		Light: lipgloss.Color("#000000"),
		Dark:  lipgloss.Color("#FFFFFF"),
	})

	// Bauhaus uses pure-yellow warn and an adaptive blue secondary: pure
	// #0000FF reads on light terms (WCAG 8.6:1) but only 2.4:1 on dark, so we
	// lift it to periwinkle on dark for legibility. Other palettes default to
	// mustard warn and pass their flag color through unchanged.
	warn := color.Color(charmtone.Mustard)
	secondary := p.Flag
	if rgbaEqual(p.Command, lipgloss.Color("#FF00AA")) {
		warn = color.Color(charmtone.Citron)
		secondary = color.Color(compat.AdaptiveColor{
			Light: lipgloss.Color("#0000FF"),
			Dark:  lipgloss.Color("#5C7BFF"),
		})
	}

	return Theme{
		Palette:   p,
		Accent:    p.Command,
		Secondary: secondary,
		Muted:     muted,
		// Error is bold magenta (same family as Bauhaus accent) for a louder,
		// brand-consistent failure signal across all palettes.
		Error:   color.Color(lipgloss.Color("#FF00AA")),
		Success: color.Color(charmtone.Guac),
		Warn:    warn,

		Title: lipgloss.NewStyle().
			Bold(true).
			Foreground(titleFg).
			Background(titleBg),
		Subtle: lipgloss.NewStyle().
			Foreground(muted),
		Bold: lipgloss.NewStyle().
			Bold(true),
	}
}

func rgbaEqual(a, b color.Color) bool {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}
