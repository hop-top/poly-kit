//go:build windows

package cli

import (
	"os"
	"time"

	"charm.land/lipgloss/v2"
)

// backgroundQueryTimeout is unused here: lipgloss owns the wait on Windows,
// where it queries once.
var backgroundQueryTimeout = 2 * time.Second

// probeBackground reports whether the background is dark. Windows needs no
// single-try variant: lipgloss queries the console once there.
func probeBackground(f *os.File) bool {
	return lipgloss.HasDarkBackground(f, f)
}
