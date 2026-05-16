package tui

import (
	"charm.land/bubbles/v2/spinner"
	"charm.land/lipgloss/v2"
	"hop.top/kit/go/console/cli"
)

// NewSpinner returns a spinner.Model pre-styled with the theme's accent color.
func NewSpinner(theme cli.Theme) spinner.Model {
	style := lipgloss.NewStyle().Foreground(theme.Accent)
	return spinner.New(
		spinner.WithSpinner(spinner.Dot),
		spinner.WithStyle(style),
	)
}
