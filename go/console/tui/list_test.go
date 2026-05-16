package tui_test

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/tui"
)

// testItem is a simple Item implementation for testing.
type testItem struct {
	text string
}

func (t testItem) Render(width int) string {
	if width > 0 && len(t.text) > width {
		return t.text[:width]
	}
	return t.text
}

func makeItems(n int) []tui.Item {
	items := make([]tui.Item, n)
	for i := range n {
		items[i] = testItem{text: fmt.Sprintf("item-%d", i)}
	}
	return items
}

func TestList_Empty(t *testing.T) {
	l := tui.NewList(5)
	assert.Equal(t, 0, len(l.Items()))
	assert.Equal(t, 0, l.Offset())
	assert.Equal(t, 5, l.Height())
	assert.Equal(t, "", l.View(80))
}

func TestList_SetItems_Render(t *testing.T) {
	l := tui.NewList(3).SetItems(makeItems(5))
	require.Equal(t, 5, len(l.Items()))

	v := l.View(80)
	assert.Contains(t, v, "item-0")
	assert.Contains(t, v, "item-1")
	assert.Contains(t, v, "item-2")
	assert.NotContains(t, v, "item-3")
}

func TestList_ScrollBy(t *testing.T) {
	l := tui.NewList(3).SetItems(makeItems(10))

	// Scroll down by 2.
	l = l.ScrollBy(2)
	assert.Equal(t, 2, l.Offset())
	v := l.View(80)
	assert.Contains(t, v, "item-2")
	assert.Contains(t, v, "item-4")
	assert.NotContains(t, v, "item-0")

	// Scroll up by 1.
	l = l.ScrollBy(-1)
	assert.Equal(t, 1, l.Offset())

	// Scroll past top clamps to 0.
	l = l.ScrollBy(-100)
	assert.Equal(t, 0, l.Offset())

	// Scroll past bottom clamps to max offset.
	l = l.ScrollBy(1000)
	assert.Equal(t, 7, l.Offset()) // 10 items - 3 height = 7
}

func TestList_ScrollToEnd(t *testing.T) {
	l := tui.NewList(3).SetItems(makeItems(10))
	l = l.ScrollToEnd()
	assert.Equal(t, 7, l.Offset())

	v := l.View(80)
	assert.Contains(t, v, "item-7")
	assert.Contains(t, v, "item-8")
	assert.Contains(t, v, "item-9")
	assert.NotContains(t, v, "item-6")
}

func TestList_Follow(t *testing.T) {
	l := tui.NewList(3).SetFollow(true)
	assert.True(t, l.Follow())

	// Add items; since list is at bottom (empty = at bottom), follow scrolls.
	l = l.SetItems(makeItems(5))
	assert.Equal(t, 2, l.Offset()) // 5-3 = 2

	// Add more items while at bottom — should auto-scroll.
	l = l.SetItems(makeItems(10))
	assert.Equal(t, 7, l.Offset()) // 10-3 = 7

	v := l.View(80)
	assert.Contains(t, v, "item-9")

	// Scroll up (no longer at bottom), then add items — should NOT auto-scroll.
	l = l.ScrollBy(-3)
	assert.Equal(t, 4, l.Offset())
	l = l.SetItems(makeItems(15))
	assert.Equal(t, 4, l.Offset()) // stays where user scrolled
}

func TestList_MouseWheel(t *testing.T) {
	l := tui.NewList(3).SetItems(makeItems(10))

	// Scroll down via mouse wheel.
	l2, cmd := l.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	assert.Nil(t, cmd)
	assert.Equal(t, 1, l2.Offset())

	// Scroll up via mouse wheel.
	l3, cmd := l2.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	assert.Nil(t, cmd)
	assert.Equal(t, 0, l3.Offset())
}

func TestList_HeightClamp(t *testing.T) {
	// Height less than 1 clamps to 1.
	l := tui.NewList(0)
	assert.Equal(t, 1, l.Height())
}

func TestList_SetHeight(t *testing.T) {
	l := tui.NewList(5).SetItems(makeItems(10)).ScrollToEnd()
	assert.Equal(t, 5, l.Offset()) // 10-5 = 5

	// Shrink height — max offset increases to 7 but current offset 5 is still valid.
	l = l.SetHeight(3)
	assert.Equal(t, 5, l.Offset())

	// Grow height past items — offset clamps down.
	l = l.SetHeight(20)
	assert.Equal(t, 0, l.Offset()) // 10-20 < 0 → 0
}

func TestList_WidthTruncation(t *testing.T) {
	l := tui.NewList(3).SetItems([]tui.Item{
		testItem{text: "a-very-long-item-text"},
	})
	v := l.View(5)
	assert.Equal(t, "a-ver", v)
}
