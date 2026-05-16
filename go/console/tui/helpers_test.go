package tui_test

import (
	"charm.land/lipgloss/v2"
	"hop.top/kit/go/console/cli"
)

// testTheme returns a deterministic theme for tests using the Neon palette.
func testTheme() cli.Theme {
	accent := lipgloss.Color("#7ED957")
	secondary := lipgloss.Color("#FF00FF")
	white := lipgloss.Color("#FFFFFF")
	muted := lipgloss.Color("#6B7280")
	errC := lipgloss.Color("#EF4444")
	success := lipgloss.Color("#10B981")

	return cli.Theme{
		Palette:   cli.Neon,
		Accent:    accent,
		Secondary: secondary,
		Muted:     muted,
		Error:     errC,
		Success:   success,
		Title:     lipgloss.NewStyle().Bold(true).Foreground(white),
		Subtle:    lipgloss.NewStyle().Foreground(muted),
		Bold:      lipgloss.NewStyle().Bold(true),
	}
}
