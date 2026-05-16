package qmochi

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func TestModel_Update(t *testing.T) {
	chart := Chart{Type: BarChart, Title: "Test"}
	m := NewModel(chart)

	// Test WindowSizeMsg
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	assert.Equal(t, 40, m2.(Model).chart.Size.Width)
	assert.Equal(t, 20, m2.(Model).chart.Size.Height)

	// Test SetChartMsg
	newChart := Chart{Type: ColumnChart, Title: "New"}
	m3, _ := m.Update(SetChartMsg{Chart: newChart})
	assert.Equal(t, ColumnChart, m3.(Model).chart.Type)
	assert.Equal(t, "New", m3.(Model).chart.Title)

	// Test SetSizeMsg
	m4, _ := m.Update(SetSizeMsg{Size: Size{Width: 10, Height: 10}})
	assert.Equal(t, 10, m4.(Model).chart.Size.Width)
}

func TestModel_View_ZeroSize(t *testing.T) {
	m := NewModel(Chart{Size: Size{Width: 0, Height: 0}})
	view := m.View()
	assert.Equal(t, "", view.Content)
}
