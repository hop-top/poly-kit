package tui_test

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/tui"
	"hop.top/kit/go/console/tui/styles"
)

func TestNewModel(t *testing.T) {
	m := tui.NewModel(testTheme(), 80, 24)

	assert.Equal(t, 80, m.Common().Width)
	assert.Equal(t, 24, m.Common().Height)
}

func TestModel_View(t *testing.T) {
	m := tui.NewModel(testTheme(), 80, 24)
	v := m.View()

	require.NotEmpty(t, v.Content, "view should not be empty")
}

func TestModel_Update_WindowSize(t *testing.T) {
	m := tui.NewModel(testTheme(), 80, 24)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	um := updated.(tui.Model)

	assert.Equal(t, 120, um.Common().Width)
	assert.Equal(t, 40, um.Common().Height)
}

func TestModel_SetContent(t *testing.T) {
	m := tui.NewModel(testTheme(), 80, 24).
		SetContent("hello world")

	v := m.View()
	assert.Contains(t, v.Content, "hello world")
}

func TestModel_Init(t *testing.T) {
	m := tui.NewModel(testTheme(), 80, 24)
	cmd := m.Init()
	assert.Nil(t, cmd, "Init should return nil cmd")
}

func TestModel_Common(t *testing.T) {
	m := tui.NewModel(testTheme(), 100, 50)
	c := m.Common()

	expected := 50 - styles.HeaderHeight - styles.FooterHeight
	assert.Equal(t, expected, c.ContentHeight())
}
