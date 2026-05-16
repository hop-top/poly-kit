package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/tui"
)

func TestNewPill(t *testing.T) {
	p := tui.NewPill("branch", "main")
	assert.Equal(t, "branch", p.Label())
	assert.Equal(t, "main", p.Value())
}

func TestPill_SetValue(t *testing.T) {
	p := tui.NewPill("branch", "main")
	p2 := p.SetValue("develop")
	assert.Equal(t, "develop", p2.Value())
	assert.Equal(t, "main", p.Value(), "original unchanged")
}

func TestPill_Render(t *testing.T) {
	theme := testTheme()
	p := tui.NewPill("branch", "main")

	focused := p.Render(theme, true)
	blurred := p.Render(theme, false)

	require.NotEmpty(t, focused)
	require.NotEmpty(t, blurred)
	assert.NotEqual(t, focused, blurred,
		"focused and blurred renders should differ")
	assert.Contains(t, focused, "branch")
	assert.Contains(t, focused, "main")
}

func TestNewPillBar(t *testing.T) {
	p1 := tui.NewPill("a", "1")
	p2 := tui.NewPill("b", "2")
	p3 := tui.NewPill("c", "3")
	bar := tui.NewPillBar(p1, p2, p3)

	assert.Len(t, bar.Pills(), 3)
	assert.Equal(t, 0, bar.FocusedIndex())
	assert.False(t, bar.Expanded())
}

func TestPillBar_Toggle(t *testing.T) {
	bar := tui.NewPillBar(tui.NewPill("a", "1"))

	bar2 := bar.Toggle()
	assert.True(t, bar2.Expanded())
	assert.False(t, bar.Expanded(), "original unchanged")

	bar3 := bar2.Toggle()
	assert.False(t, bar3.Expanded())
}

func TestPillBar_FocusNext_FocusPrev(t *testing.T) {
	bar := tui.NewPillBar(
		tui.NewPill("a", "1"),
		tui.NewPill("b", "2"),
		tui.NewPill("c", "3"),
	)

	assert.Equal(t, 0, bar.FocusedIndex())

	bar = bar.FocusNext()
	assert.Equal(t, 1, bar.FocusedIndex())

	bar = bar.FocusNext()
	assert.Equal(t, 2, bar.FocusedIndex())

	// Wrap around forward.
	bar = bar.FocusNext()
	assert.Equal(t, 0, bar.FocusedIndex())

	// Wrap around backward.
	bar = bar.FocusPrev()
	assert.Equal(t, 2, bar.FocusedIndex())

	bar = bar.FocusPrev()
	assert.Equal(t, 1, bar.FocusedIndex())
}

func TestPillBar_View_Compact(t *testing.T) {
	theme := testTheme()
	bar := tui.NewPillBar(
		tui.NewPill("branch", "main"),
		tui.NewPill("commit", "abc123"),
	)

	view := bar.ViewWithTheme(theme, 80)
	require.NotEmpty(t, view)
	assert.Contains(t, view, "branch")
	assert.Contains(t, view, "main")
	assert.Contains(t, view, "commit")
	assert.Contains(t, view, "abc123")
	assert.Contains(t, view, "ctrl+t")
	assert.Contains(t, view, "open")
}

func TestPillBar_View_Expanded(t *testing.T) {
	theme := testTheme()
	bar := tui.NewPillBar(
		tui.NewPill("branch", "main"),
	)
	bar = bar.SetExpanded("detailed info here")
	bar = bar.Toggle()

	view := bar.ViewWithTheme(theme, 80)
	require.NotEmpty(t, view)
	assert.Contains(t, view, "branch")
	assert.Contains(t, view, "detailed info here")
	assert.Contains(t, view, "close")
}

func TestPillBar_Update_ToggleKey(t *testing.T) {
	bar := tui.NewPillBar(tui.NewPill("a", "1"))
	assert.False(t, bar.Expanded())

	msg := tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl}
	bar, _ = bar.Update(msg)
	assert.True(t, bar.Expanded())

	bar, _ = bar.Update(msg)
	assert.False(t, bar.Expanded())
}

func TestPillBar_Update_ArrowKeys(t *testing.T) {
	bar := tui.NewPillBar(
		tui.NewPill("a", "1"),
		tui.NewPill("b", "2"),
		tui.NewPill("c", "3"),
	)
	bar = bar.Toggle() // expand first

	// Right arrow moves focus forward.
	right := tea.KeyPressMsg{Code: tea.KeyRight}
	bar, _ = bar.Update(right)
	assert.Equal(t, 1, bar.FocusedIndex())

	bar, _ = bar.Update(right)
	assert.Equal(t, 2, bar.FocusedIndex())

	// Left arrow moves focus backward.
	left := tea.KeyPressMsg{Code: tea.KeyLeft}
	bar, _ = bar.Update(left)
	assert.Equal(t, 1, bar.FocusedIndex())
}

func TestPillBar_Update_ArrowKeys_NotExpanded(t *testing.T) {
	bar := tui.NewPillBar(
		tui.NewPill("a", "1"),
		tui.NewPill("b", "2"),
	)
	// Not expanded: arrows should not change focus.
	right := tea.KeyPressMsg{Code: tea.KeyRight}
	bar, _ = bar.Update(right)
	assert.Equal(t, 0, bar.FocusedIndex())
}

func TestPillBar_Height(t *testing.T) {
	bar := tui.NewPillBar(tui.NewPill("a", "1"))

	// Compact.
	assert.Equal(t, 1, bar.Height())

	// Expanded without content.
	bar = bar.Toggle()
	assert.Equal(t, 1, bar.Height())

	// Expanded with single-line content.
	bar = bar.SetExpanded("details")
	assert.Equal(t, 2, bar.Height())

	// Expanded with multi-line content.
	bar = bar.SetExpanded(strings.Join([]string{"line1", "line2", "line3"}, "\n"))
	assert.Equal(t, 4, bar.Height())
}
