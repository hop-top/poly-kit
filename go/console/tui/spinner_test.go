package tui_test

import (
	"testing"

	"charm.land/bubbles/v2/spinner"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/tui"
)

func TestNewSpinner(t *testing.T) {
	s := tui.NewSpinner(testTheme())

	require.Equal(t, spinner.Dot, s.Spinner, "default spinner should be Dot")
	require.NotEmpty(t, s.View(), "initial view should not be empty")
}
