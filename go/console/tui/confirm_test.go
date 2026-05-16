package tui_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/tui"
)

func TestConfirm_AcceptY(t *testing.T) {
	c := tui.NewConfirm("Delete?", testTheme())
	require.False(t, c.Done())

	m, _ := c.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	confirm := m.(tui.Confirm)
	assert.True(t, confirm.Accepted())
	assert.True(t, confirm.Done())
}

func TestConfirm_RejectN(t *testing.T) {
	c := tui.NewConfirm("Delete?", testTheme())
	m, _ := c.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	confirm := m.(tui.Confirm)
	assert.False(t, confirm.Accepted())
	assert.True(t, confirm.Done())
}

func TestConfirm_RejectEsc(t *testing.T) {
	c := tui.NewConfirm("Delete?", testTheme())
	m, _ := c.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	confirm := m.(tui.Confirm)
	assert.False(t, confirm.Accepted())
	assert.True(t, confirm.Done())
}

func TestConfirm_AcceptEnter(t *testing.T) {
	c := tui.NewConfirm("Proceed?", testTheme())
	m, _ := c.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	confirm := m.(tui.Confirm)
	assert.True(t, confirm.Accepted())
	assert.True(t, confirm.Done())
}

func TestConfirm_ViewBeforeChoice(t *testing.T) {
	c := tui.NewConfirm("Continue?", testTheme())
	v := c.View()
	assert.NotZero(t, v)
}

func TestConfirm_IgnoreUnrelatedKey(t *testing.T) {
	c := tui.NewConfirm("Delete?", testTheme())
	m, _ := c.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	confirm := m.(tui.Confirm)
	assert.False(t, confirm.Done())
}

func TestConfirm_NoUpdateAfterDone(t *testing.T) {
	c := tui.NewConfirm("Delete?", testTheme())
	m, _ := c.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	confirm := m.(tui.Confirm)
	require.True(t, confirm.Done())

	// Further input should be ignored.
	m2, _ := confirm.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	confirm2 := m2.(tui.Confirm)
	assert.True(t, confirm2.Accepted(), "should still be accepted after done")
}
