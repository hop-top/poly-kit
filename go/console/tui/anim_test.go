package tui_test

import (
	"image/color"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/tui"
)

func TestNewAnim_Defaults(t *testing.T) {
	a := tui.NewAnim(tui.AnimSettings{})
	assert.Equal(t, 10, a.Width(), "default width should be 10 (no label)")
}

func TestNewAnim_CustomWidth(t *testing.T) {
	a := tui.NewAnim(tui.AnimSettings{Width: 5})
	assert.Equal(t, 5, a.Width())
}

func TestAnim_Start(t *testing.T) {
	a := tui.NewAnim(tui.AnimSettings{})
	cmd := a.Start()
	require.NotNil(t, cmd, "Start should return a non-nil cmd")
}

func TestAnim_Animate_MatchingID(t *testing.T) {
	a := tui.NewAnim(tui.AnimSettings{})
	_ = a.Start()

	// We need to send the correct ID. Since IDs are auto-incremented,
	// we create a fresh anim and get its view before/after.
	viewBefore := a.View()

	// Simulate matching message — we need to extract the ID from the anim.
	// The Animate method only advances on matching ID.
	// We'll use a brute approach: create anim, start, then call Animate
	// with a constructed msg. Since ID is private, we rely on the cmd
	// producing the right AnimStepMsg.
	a2 := tui.NewAnim(tui.AnimSettings{Width: 3})
	cmd := a2.Start()
	require.NotNil(t, cmd)

	// Execute the cmd to get the message.
	msg := cmd()
	stepMsg, ok := msg.(tui.AnimStepMsg)
	require.True(t, ok, "cmd should produce AnimStepMsg")

	a3, cmd2 := a2.Animate(stepMsg)
	require.NotNil(t, cmd2, "matching ID should produce next tick cmd")
	_ = a3
	_ = viewBefore
}

func TestAnim_Animate_WrongID(t *testing.T) {
	a := tui.NewAnim(tui.AnimSettings{})
	// Use an ID that won't match.
	_, cmd := a.Animate(tui.AnimStepMsg{ID: -999})
	assert.Nil(t, cmd, "wrong ID should return nil cmd")
}

func TestAnim_View_NotEmpty(t *testing.T) {
	a := tui.NewAnim(tui.AnimSettings{
		GradColorA: color.RGBA{R: 100, G: 200, B: 50, A: 255},
		GradColorB: color.RGBA{R: 200, G: 50, B: 100, A: 255},
	})
	v := a.View()
	assert.NotEmpty(t, v, "View should produce non-empty output")
}

func TestAnim_View_Width(t *testing.T) {
	theme := testTheme()
	label := " loading"
	a := tui.NewAnim(tui.AnimSettings{
		Width:      6,
		Label:      label,
		GradColorA: theme.Accent,
		GradColorB: theme.Secondary,
	})
	assert.Equal(t, 6+len([]rune(label)), a.Width(),
		"Width should be cycling chars + label rune count")
}

func TestAnim_SetLabel(t *testing.T) {
	a := tui.NewAnim(tui.AnimSettings{Width: 4})
	a2 := a.SetLabel("test")
	assert.Equal(t, 4+4, a2.Width(), "Width should include new label")

	a3 := a2.SetLabel("")
	assert.Equal(t, 4, a3.Width(), "Width should exclude empty label")
}

func TestAnim_Tick(t *testing.T) {
	a := tui.NewAnim(tui.AnimSettings{})
	// Verify Animatable interface satisfaction.
	var _ tui.Animatable = a
	cmd := a.Tick()
	require.NotNil(t, cmd, "Tick should return non-nil cmd")
}

func TestMakeGradient(t *testing.T) {
	a := color.RGBA{R: 255, G: 0, B: 0, A: 255}
	b := color.RGBA{R: 0, G: 0, B: 255, A: 255}
	grad := tui.MakeGradient(5, a, b)
	require.Len(t, grad, 5, "gradient should have requested size")

	// First and last should approximate the input colors.
	r0, _, _, _ := grad[0].RGBA()
	assert.Greater(t, r0, uint32(200<<8), "first color should be reddish")

	_, _, b4, _ := grad[4].RGBA()
	assert.Greater(t, b4, uint32(100<<8), "last color should be bluish")
}

func TestMakeGradient_SingleElement(t *testing.T) {
	a := color.RGBA{R: 128, G: 64, B: 32, A: 255}
	grad := tui.MakeGradient(1, a, a)
	require.Len(t, grad, 1)
}

func TestMakeGradient_Zero(t *testing.T) {
	grad := tui.MakeGradient(0, nil, nil)
	assert.Nil(t, grad)
}
