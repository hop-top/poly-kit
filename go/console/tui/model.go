package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/tui/styles"
)

// Model is the top-level bubbletea model for a TUI application.
// It owns the Common context (styles + terminal dimensions) and
// dispatches messages to pluggable sub-models.
type Model struct {
	common styles.Common

	headerText string
	footerText string
	content    string
}

// NewModel returns a Model wired to the given theme and terminal size.
func NewModel(theme cli.Theme, width, height int) Model {
	return Model{
		common:     styles.NewCommon(theme, width, height),
		headerText: "hop",
		footerText: "q: quit",
		content:    "ready",
	}
}

// Common returns the shared context so sub-models can access styles
// and terminal dimensions.
func (m Model) Common() styles.Common { return m.common }

// Init satisfies tea.Model. No initial commands.
func (m Model) Init() tea.Cmd { return nil }

// Update satisfies tea.Model. It handles window-size changes and quit
// keys, then delegates remaining messages to sub-models.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.common.Width = msg.Width
		m.common.Height = msg.Height
		return m, nil

	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}

	return m, nil
}

// View satisfies tea.Model. It composes header, main content, and footer
// into a single view using the layout styles from Common.
func (m Model) View() tea.View {
	s := m.common.Styles
	w := m.common.Width

	header := s.Header.Width(w).Render(m.headerText)
	footer := s.Footer.Width(w).Render(m.footerText)

	contentH := m.common.ContentHeight()
	main := s.Main.
		Width(w).
		Height(contentH).
		Render(m.content)

	out := strings.Join([]string{header, main, footer}, "\n")
	return tea.NewView(lipgloss.Place(w, m.common.Height, lipgloss.Left, lipgloss.Top, out))
}

// SetHeader returns a copy with the given header text.
func (m Model) SetHeader(text string) Model {
	m.headerText = text
	return m
}

// SetFooter returns a copy with the given footer text.
func (m Model) SetFooter(text string) Model {
	m.footerText = text
	return m
}

// SetContent returns a copy with the given main content text.
func (m Model) SetContent(text string) Model {
	m.content = text
	return m
}
