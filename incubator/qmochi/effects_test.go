package qmochi

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
)

func TestApplyEffects(t *testing.T) {
	style := lipgloss.NewStyle()

	// Test Blink
	blinkStyle := ApplyEffects(style, BlinkEffect)
	// We can't easily check internal state of lipgloss.Style, but we can verify it doesn't panic
	_ = blinkStyle.Render("test")

	// Test None
	noneStyle := ApplyEffects(style, NoEffect)
	assert.Equal(t, style, noneStyle)
}

func TestGetPointStyle(t *testing.T) {
	s := Series{Color: "#FF0000", Effect: BlinkEffect}
	p := Point{Value: 10}

	style := GetPointStyle(p, s, false)
	_ = style.Render("test")

	// Test point override
	p2 := Point{Color: "#00FF00", Effect: NoEffect}
	style2 := GetPointStyle(p2, s, false)
	_ = style2.Render("test")
}
