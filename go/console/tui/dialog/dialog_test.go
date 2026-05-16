package dialog_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/tui/dialog"
)

// mockDialog is a test Dialog that tracks update calls.
type mockDialog struct {
	content string
	done    bool
	updated bool
	lastMsg tea.Msg
}

func (d *mockDialog) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd) {
	d.updated = true
	d.lastMsg = msg
	return d, nil
}

func (d *mockDialog) View(width, height int) string {
	return d.content
}

func (d *mockDialog) Done() bool {
	return d.done
}

// doneAfterUpdate completes on first update.
type doneAfterUpdate struct {
	content string
	done    bool
}

func (d *doneAfterUpdate) Update(msg tea.Msg) (dialog.Dialog, tea.Cmd) {
	d.done = true
	return d, nil
}

func (d *doneAfterUpdate) View(width, height int) string {
	return d.content
}

func (d *doneAfterUpdate) Done() bool {
	return d.done
}

func TestOverlay_Empty(t *testing.T) {
	o := dialog.NewOverlay()
	assert.False(t, o.IsActive())
	assert.Nil(t, o.Active())

	// View passes base through unchanged.
	base := "hello\nworld"
	v := o.View(base, 10, 2)
	assert.Equal(t, base, v)

	// Update on empty overlay is a no-op.
	o2, cmd := o.Update(tea.KeyPressMsg{})
	assert.Nil(t, cmd)
	assert.False(t, o2.IsActive())
}

func TestOverlay_PushPop(t *testing.T) {
	o := dialog.NewOverlay()

	d1 := &mockDialog{content: "dialog-1"}
	d2 := &mockDialog{content: "dialog-2"}

	o = o.Push(d1)
	require.True(t, o.IsActive())
	assert.Equal(t, d1, o.Active())

	o = o.Push(d2)
	assert.Equal(t, d2, o.Active())

	o = o.Pop()
	assert.Equal(t, d1, o.Active())

	o = o.Pop()
	assert.False(t, o.IsActive())

	// Pop on empty is safe.
	o = o.Pop()
	assert.False(t, o.IsActive())
}

func TestOverlay_UpdateRoutesToActive(t *testing.T) {
	o := dialog.NewOverlay()
	d1 := &mockDialog{content: "first"}
	d2 := &mockDialog{content: "second"}

	o = o.Push(d1).Push(d2)

	msg := tea.KeyPressMsg{}
	_, _ = o.Update(msg)

	// Only the top dialog should receive the message.
	assert.True(t, d2.updated)
	assert.False(t, d1.updated)
}

func TestOverlay_PopOnDone(t *testing.T) {
	o := dialog.NewOverlay()
	d := &doneAfterUpdate{content: "done-dialog"}

	o = o.Push(d)
	require.True(t, o.IsActive())

	// Update triggers Done() → auto-pop.
	o, _ = o.Update(tea.KeyPressMsg{})
	assert.False(t, o.IsActive())
}

func TestOverlay_ViewCentersDialog(t *testing.T) {
	o := dialog.NewOverlay()
	d := &mockDialog{content: "X"}

	o = o.Push(d)

	// 10x5 grid, dialog "X" is 1x1 → centered at (4, 2).
	base := strings.Repeat(strings.Repeat(".", 10)+"\n", 4) +
		strings.Repeat(".", 10)
	v := o.View(base, 10, 5)

	lines := strings.Split(v, "\n")
	require.Equal(t, 5, len(lines))

	// The dialog "X" should appear at row 2, column 4.
	assert.Equal(t, byte('X'), lines[2][4])

	// Surrounding cells should be dots or spaces.
	assert.NotEqual(t, byte('X'), lines[0][4])
}

func TestOverlay_MultiLineDialog(t *testing.T) {
	o := dialog.NewOverlay()
	d := &mockDialog{content: "AB\nCD"}

	o = o.Push(d)

	base := strings.Repeat(strings.Repeat(".", 10)+"\n", 4) +
		strings.Repeat(".", 10)
	v := o.View(base, 10, 5)
	lines := strings.Split(v, "\n")

	// 2x2 dialog in 10x5 grid → top-left at (4, 1).
	assert.Equal(t, byte('A'), lines[1][4])
	assert.Equal(t, byte('B'), lines[1][5])
	assert.Equal(t, byte('C'), lines[2][4])
	assert.Equal(t, byte('D'), lines[2][5])
}

func TestOverlay_PushImmutability(t *testing.T) {
	o1 := dialog.NewOverlay()
	d := &mockDialog{content: "d"}

	o2 := o1.Push(d)
	assert.False(t, o1.IsActive(), "original should be unchanged")
	assert.True(t, o2.IsActive())
}
