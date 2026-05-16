package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"hop.top/kit/go/console/cli"
)

// Confirm is a yes/no confirmation prompt implementing tea.Model.
type Confirm struct {
	prompt   string
	accepted bool
	done     bool

	promptStyle lipgloss.Style
	yesStyle    lipgloss.Style
	noStyle     lipgloss.Style
	hintStyle   lipgloss.Style
}

// NewConfirm returns a Confirm model styled with the given theme.
func NewConfirm(prompt string, theme cli.Theme) Confirm {
	return Confirm{
		prompt:      prompt,
		promptStyle: theme.Bold.Foreground(theme.Accent),
		yesStyle:    lipgloss.NewStyle().Foreground(theme.Success),
		noStyle:     lipgloss.NewStyle().Foreground(theme.Error),
		hintStyle:   lipgloss.NewStyle().Foreground(theme.Muted),
	}
}

// Accepted returns true when the user confirmed with y/Y/enter.
func (c Confirm) Accepted() bool { return c.accepted }

// Done returns true when the user has made a choice.
func (c Confirm) Done() bool { return c.done }

// Init satisfies tea.Model.
func (c Confirm) Init() tea.Cmd { return nil }

// Update satisfies tea.Model.
func (c Confirm) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if c.done {
		return c, nil
	}

	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "y", "Y", "enter":
			c.accepted = true
			c.done = true
			return c, tea.Quit
		case "n", "N", "esc", "q":
			c.accepted = false
			c.done = true
			return c, tea.Quit
		}
	}
	return c, nil
}

// View satisfies tea.Model.
func (c Confirm) View() tea.View {
	if c.done {
		if c.accepted {
			return tea.NewView(c.promptStyle.Render(c.prompt) + " " +
				c.yesStyle.Render("Yes"))
		}
		return tea.NewView(c.promptStyle.Render(c.prompt) + " " +
			c.noStyle.Render("No"))
	}
	return tea.NewView(c.promptStyle.Render(c.prompt) + " " +
		c.hintStyle.Render("[y/n]"))
}
