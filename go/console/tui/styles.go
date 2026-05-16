package tui

import (
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
	"hop.top/kit/go/console/cli"
)

// TableStyles returns a table.Styles themed with the palette.
func TableStyles(t cli.Theme) table.Styles {
	return table.Styles{
		Header: lipgloss.NewStyle().
			Bold(true).
			Foreground(t.Secondary).
			Padding(0, 1),
		Cell: lipgloss.NewStyle().
			Padding(0, 1),
		Selected: lipgloss.NewStyle().
			Bold(true).
			Foreground(t.Accent),
	}
}

// TextInputStyles returns a textinput.Styles themed with the palette.
func TextInputStyles(t cli.Theme) textinput.Styles {
	return textinput.Styles{
		Focused: textinput.StyleState{
			Prompt:      lipgloss.NewStyle().Foreground(t.Accent),
			Text:        lipgloss.NewStyle(),
			Placeholder: lipgloss.NewStyle().Foreground(t.Muted),
			Suggestion:  lipgloss.NewStyle().Foreground(t.Muted),
		},
		Blurred: textinput.StyleState{
			Prompt:      lipgloss.NewStyle().Foreground(t.Muted),
			Text:        lipgloss.NewStyle().Foreground(t.Muted),
			Placeholder: lipgloss.NewStyle().Foreground(t.Muted),
			Suggestion:  lipgloss.NewStyle().Foreground(t.Muted),
		},
		Cursor: textinput.CursorStyle{
			Color: t.Accent,
		},
	}
}

// ListStyles returns a list.Styles themed with the palette.
func ListStyles(t cli.Theme) list.Styles {
	s := list.DefaultStyles(true)
	s.Title = s.Title.Foreground(t.Accent)
	s.TitleBar = s.TitleBar.Foreground(t.Accent)
	s.Spinner = lipgloss.NewStyle().Foreground(t.Accent)
	s.DefaultFilterCharacterMatch = lipgloss.NewStyle().
		Foreground(t.Secondary)
	s.StatusBar = s.StatusBar.Foreground(t.Muted)
	s.StatusBarActiveFilter = s.StatusBarActiveFilter.
		Foreground(t.Secondary)
	return s
}
