package tui

import (
	"context"
	"fmt"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/core/upgrade"
)

// CheckDoneMsg is sent when the background version check completes.
type CheckDoneMsg struct {
	Result *upgrade.Result
}

// StartCheckCmd returns a tea.Cmd that runs an upgrade check in the background.
func StartCheckCmd(c *upgrade.Checker) tea.Cmd {
	return func() tea.Msg {
		r := c.Check(context.Background())
		return CheckDoneMsg{Result: r}
	}
}

// Badge is a bubbletea model that renders an "update available" badge.
// Returns empty view while the background check runs; shows the badge once done.
type Badge struct {
	checker *upgrade.Checker
	result  *upgrade.Result
	spin    spinner.Model
	loading bool

	badgeStyle  lipgloss.Style
	noticeStyle lipgloss.Style
}

// NewBadge returns a Badge wired to checker and styled with theme.
func NewBadge(checker *upgrade.Checker, theme cli.Theme) Badge {
	return Badge{
		checker: checker,
		spin:    NewSpinner(theme),
		loading: true,
		badgeStyle: theme.Bold.
			Foreground(theme.Accent).
			Padding(0, 1),
		noticeStyle: lipgloss.NewStyle().
			Foreground(theme.Success).
			Italic(true),
	}
}

// Init starts the background version check and the spinner tick.
func (b Badge) Init() tea.Cmd {
	return tea.Batch(b.spin.Tick, StartCheckCmd(b.checker))
}

// Update handles CheckDoneMsg and spinner ticks.
func (b Badge) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case CheckDoneMsg:
		b.loading = false
		b.result = m.Result
		return b, nil
	case spinner.TickMsg:
		if b.loading {
			var cmd tea.Cmd
			b.spin, cmd = b.spin.Update(m)
			return b, cmd
		}
	}
	return b, nil
}

// View renders the badge. Returns empty view when loading or no update.
func (b Badge) View() tea.View {
	if b.loading {
		return tea.NewView("")
	}
	if b.result == nil || b.result.Err != nil || !b.result.UpdateAvail {
		return tea.NewView("")
	}
	badge := b.badgeStyle.Render("^ UPDATE")
	notice := b.noticeStyle.Render(
		fmt.Sprintf(" %s -> %s", b.result.Current, b.result.Latest),
	)
	return tea.NewView(badge + notice + "\n")
}

// Loading reports whether the background check is still in progress.
func (b Badge) Loading() bool { return b.loading }

// Result returns the upgrade check result, or nil if still loading.
func (b Badge) Result() *upgrade.Result { return b.result }
