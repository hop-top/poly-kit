package tui_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"hop.top/kit/go/console/tui"
)

// stubItem implements tui.Renderer (= tui.Item).
type stubItem struct{ label string }

func (s stubItem) Render(width int) string {
	if width > 0 && len(s.label) > width {
		return s.label[:width]
	}
	return s.label
}

var _ tui.Renderer = stubItem{}

func TestItem_ImplementsRenderer(t *testing.T) {
	var r tui.Renderer = stubItem{label: "hello"}
	got := r.Render(80)
	assert.Equal(t, "hello", got)

	// Also usable as Item in a List.
	l := tui.NewList(5).SetItems([]tui.Item{
		stubItem{label: "a"},
		stubItem{label: "b"},
	})
	assert.Equal(t, 2, len(l.Items()))
}

// stubAnimatable is a minimal Animatable for testing the interface.
type stubAnimatable struct{}

func (stubAnimatable) Tick() tea.Cmd { return nil }

var _ tui.Animatable = stubAnimatable{}

func TestAnimatable_Interface(t *testing.T) {
	var a tui.Animatable = stubAnimatable{}
	cmd := a.Tick()
	assert.Nil(t, cmd)
}

func TestInterfaces_Usable(t *testing.T) {
	// Renderer is usable with width constraint.
	var r tui.Renderer = stubItem{label: "long-text-here"}
	assert.Equal(t, "long", r.Render(4))

	// Animatable returns a command.
	var a tui.Animatable = stubAnimatable{}
	assert.Nil(t, a.Tick())
}
