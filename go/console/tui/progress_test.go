package tui_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/tui"
)

func TestNewProgress_ZeroPercent(t *testing.T) {
	p := tui.NewProgress(testTheme())
	v := p.View()
	require.NotEmpty(t, v)
	// At 0% the bar should contain only empty chars (no filled).
	assert.NotContains(t, stripANSI(v), "█")
}

func TestProgress_SetPercent(t *testing.T) {
	p := tui.NewProgress(testTheme()).SetPercent(0.5)
	assert.InDelta(t, 0.5, p.Percent(), 0.001)
}

func TestProgress_ClampPercent(t *testing.T) {
	p := tui.NewProgress(testTheme())
	assert.InDelta(t, 0.0, p.SetPercent(-1).Percent(), 0.001)
	assert.InDelta(t, 1.0, p.SetPercent(2).Percent(), 0.001)
}

func TestProgress_Update(t *testing.T) {
	p := tui.NewProgress(testTheme())
	p2, cmd := p.Update(tui.ProgressMsg(0.75))
	assert.Nil(t, cmd)
	assert.InDelta(t, 0.75, p2.Percent(), 0.001)
}

func TestProgress_FullWidth(t *testing.T) {
	p := tui.NewProgress(testTheme()).SetPercent(1.0)
	v := stripANSI(p.View())
	assert.False(t, strings.Contains(v, "░"), "fully filled bar should have no empty chars")
}

// stripANSI removes ANSI escape sequences for assertion clarity.
func stripANSI(s string) string {
	var out strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\033' {
			inEsc = true
			continue
		}
		if inEsc {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
