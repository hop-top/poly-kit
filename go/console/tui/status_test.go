package tui_test

import (
	"testing"

	"charm.land/bubbles/v2/key"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/tui"
)

// testKeyMap implements help.KeyMap for tests.
type testKeyMap struct {
	Quit key.Binding
	Help key.Binding
}

func (k testKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Quit, k.Help}
}

func (k testKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Quit, k.Help}}
}

func defaultTestKeyMap() testKeyMap {
	return testKeyMap{
		Quit: key.NewBinding(
			key.WithKeys("q"),
			key.WithHelp("q", "quit"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
	}
}

func TestNewStatus(t *testing.T) {
	s := tui.NewStatus(testTheme(), defaultTestKeyMap())

	assert.False(t, s.ShowingAll(), "default should be compact")
	// View should produce non-empty output with bindings.
	out := s.View(80)
	require.NotEmpty(t, out)
}

func TestStatus_View(t *testing.T) {
	s := tui.NewStatus(testTheme(), defaultTestKeyMap())
	out := s.View(80)

	assert.Contains(t, out, "quit")
	assert.Contains(t, out, "help")
}

func TestStatus_ToggleHelp(t *testing.T) {
	s := tui.NewStatus(testTheme(), defaultTestKeyMap())

	assert.False(t, s.ShowingAll())
	s = s.ToggleHelp()
	assert.True(t, s.ShowingAll())
	s = s.ToggleHelp()
	assert.False(t, s.ShowingAll())
}

func TestStatus_InfoMsg(t *testing.T) {
	s := tui.NewStatus(testTheme(), defaultTestKeyMap())

	// Set an info message; it should render over help.
	s = s.SetInfoMsg(tui.InfoMsg{
		Type: tui.InfoTypeError,
		Msg:  "something broke",
	})
	out := s.View(80)
	assert.Contains(t, out, "something broke")
	assert.NotContains(t, out, "quit",
		"help line should be hidden while info msg is set")

	// Clear restores help.
	s = s.ClearInfoMsg()
	out = s.View(80)
	assert.Contains(t, out, "quit")
}

func TestStatus_Update_ClearStatusMsg(t *testing.T) {
	s := tui.NewStatus(testTheme(), defaultTestKeyMap())
	s = s.SetInfoMsg(tui.InfoMsg{
		Type: tui.InfoTypeWarn,
		Msg:  "heads up",
	})

	// Simulate ClearStatusMsg delivery.
	s, cmd := s.Update(tui.ClearStatusMsg{})
	assert.Nil(t, cmd)

	out := s.View(80)
	assert.NotContains(t, out, "heads up",
		"info msg should be cleared after ClearStatusMsg")
	assert.Contains(t, out, "quit",
		"help line should be restored")
}

func TestStatus_SetWidth(t *testing.T) {
	s := tui.NewStatus(testTheme(), defaultTestKeyMap())
	s = s.SetWidth(40)

	// Verify it does not panic and produces output.
	out := s.View(40)
	require.NotEmpty(t, out)
}

func TestStatus_InfoTypes(t *testing.T) {
	km := defaultTestKeyMap()
	types := []tui.InfoType{
		tui.InfoTypeInfo,
		tui.InfoTypeError,
		tui.InfoTypeWarn,
		tui.InfoTypeSuccess,
	}

	for _, it := range types {
		s := tui.NewStatus(testTheme(), km)
		s = s.SetInfoMsg(tui.InfoMsg{Type: it, Msg: "test"})
		out := s.View(80)
		assert.Contains(t, out, "test",
			"info type %d should render message", it)
	}
}
