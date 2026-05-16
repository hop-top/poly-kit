// Package styles provides a central styling and layout engine for TUI apps.
//
// Styles wraps semantic colors derived from cli.Theme into lipgloss styles.
// Common threads those styles together with terminal dimensions so that
// every component can lay itself out relative to the available space.
package styles

import (
	"charm.land/lipgloss/v2"
	"hop.top/kit/contracts/parity"
	"hop.top/kit/go/console/cli"
)

// Styles holds semantic lipgloss styles derived from a cli.Theme.
// Components should reference these styles rather than building their
// own from raw color values.
type Styles struct {
	// Semantic text styles.
	Accent    lipgloss.Style
	Secondary lipgloss.Style
	Muted     lipgloss.Style
	Error     lipgloss.Style
	Success   lipgloss.Style
	Title     lipgloss.Style
	Subtle    lipgloss.Style
	Bold      lipgloss.Style

	// Layout region styles.
	Header  lipgloss.Style
	Sidebar lipgloss.Style
	Main    lipgloss.Style
	Footer  lipgloss.Style

	// Status bar styles.
	Status StatusStyles

	// Pills component styles.
	Pills PillsStyles
}

// PillsStyles holds styles for the Pills component.
type PillsStyles struct {
	Focused  lipgloss.Style // pill with rounded border
	Blurred  lipgloss.Style // pill with hidden border
	HelpKey  lipgloss.Style // muted style for shortcut hint key
	HelpText lipgloss.Style // subtle style for shortcut hint text
	Area     lipgloss.Style // container padding
}

// StatusStyles holds styles for the Status component's info messages.
type StatusStyles struct {
	Help             lipgloss.Style
	ErrorIndicator   lipgloss.Style
	ErrorMessage     lipgloss.Style
	WarnIndicator    lipgloss.Style
	WarnMessage      lipgloss.Style
	InfoIndicator    lipgloss.Style
	InfoMessage      lipgloss.Style
	SuccessIndicator lipgloss.Style
	SuccessMessage   lipgloss.Style
}

// NewStyles builds a Styles from the given cli.Theme.
func NewStyles(theme cli.Theme) Styles {
	return Styles{
		Accent:    lipgloss.NewStyle().Foreground(theme.Accent),
		Secondary: lipgloss.NewStyle().Foreground(theme.Secondary),
		Muted:     lipgloss.NewStyle().Foreground(theme.Muted),
		Error:     lipgloss.NewStyle().Foreground(theme.Error),
		Success:   lipgloss.NewStyle().Foreground(theme.Success),
		Title:     theme.Title,
		Subtle:    theme.Subtle,
		Bold:      theme.Bold,

		Header: lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.Accent).
			Padding(0, 1),

		Sidebar: lipgloss.NewStyle().
			Foreground(theme.Muted).
			Padding(1, 2),

		Main: lipgloss.NewStyle().
			Padding(1, 2),

		Footer: lipgloss.NewStyle().
			Foreground(theme.Muted).
			Padding(0, 1),

		Pills: PillsStyles{
			Focused: lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(theme.Muted).
				Padding(0, 1),
			Blurred: lipgloss.NewStyle().
				Border(lipgloss.HiddenBorder()).
				Padding(0, 1),
			HelpKey: lipgloss.NewStyle().
				Foreground(theme.Muted),
			HelpText: lipgloss.NewStyle().
				Foreground(theme.Muted).Faint(true),
			Area: lipgloss.NewStyle().
				Padding(0, 1),
		},

		Status: StatusStyles{
			Help: lipgloss.NewStyle().
				Foreground(theme.Muted),
			ErrorIndicator: lipgloss.NewStyle().
				Foreground(theme.Error).SetString(parity.Values.Status.Symbols["error"] + " "),
			ErrorMessage: lipgloss.NewStyle().
				Foreground(theme.Error),
			WarnIndicator: lipgloss.NewStyle().
				Foreground(theme.Secondary).SetString(parity.Values.Status.Symbols["warn"] + " "),
			WarnMessage: lipgloss.NewStyle().
				Foreground(theme.Secondary),
			InfoIndicator: lipgloss.NewStyle().
				Foreground(theme.Accent).SetString(parity.Values.Status.Symbols["info"] + " "),
			InfoMessage: lipgloss.NewStyle().
				Foreground(theme.Accent),
			SuccessIndicator: lipgloss.NewStyle().
				Foreground(theme.Success).SetString(parity.Values.Status.Symbols["success"] + " "),
			SuccessMessage: lipgloss.NewStyle().
				Foreground(theme.Success),
		},
	}
}

// Common threads styles and terminal dimensions through every component
// in the TUI tree. Pass Common by value to sub-models so they can
// render relative to the available space.
type Common struct {
	Styles Styles
	Width  int
	Height int
}

// NewCommon returns a Common context for the given theme and terminal size.
func NewCommon(theme cli.Theme, width, height int) Common {
	return Common{
		Styles: NewStyles(theme),
		Width:  width,
		Height: height,
	}
}

// HeaderHeight is the fixed number of rows reserved for the header.
const HeaderHeight = 1

// FooterHeight is the fixed number of rows reserved for the footer.
const FooterHeight = 1

// ContentHeight returns the number of rows available for the main area
// after subtracting header and footer.
func (c Common) ContentHeight() int {
	h := c.Height - HeaderHeight - FooterHeight
	if h < 0 {
		return 0
	}
	return h
}
