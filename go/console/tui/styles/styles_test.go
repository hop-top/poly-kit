package styles_test

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/tui/styles"
)

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

func TestNewStyles(t *testing.T) {
	s := styles.NewStyles(testTheme())

	// Semantic styles should produce non-empty rendered output.
	require.NotEmpty(t, s.Accent.Render("x"))
	require.NotEmpty(t, s.Secondary.Render("x"))
	require.NotEmpty(t, s.Muted.Render("x"))
	require.NotEmpty(t, s.Error.Render("x"))
	require.NotEmpty(t, s.Success.Render("x"))

	// Layout region styles should render without panic.
	require.NotEmpty(t, s.Header.Render("h"))
	require.NotEmpty(t, s.Sidebar.Render("s"))
	require.NotEmpty(t, s.Main.Render("m"))
	require.NotEmpty(t, s.Footer.Render("f"))
}

func TestNewCommon(t *testing.T) {
	c := styles.NewCommon(testTheme(), 80, 24)

	assert.Equal(t, 80, c.Width)
	assert.Equal(t, 24, c.Height)

	// Content height = total - header - footer.
	expected := 24 - styles.HeaderHeight - styles.FooterHeight
	assert.Equal(t, expected, c.ContentHeight())
}

func TestContentHeight_ZeroTerminal(t *testing.T) {
	c := styles.NewCommon(testTheme(), 80, 0)
	assert.Equal(t, 0, c.ContentHeight(), "should clamp to 0")
}
