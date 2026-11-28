//go:build !unix

package cli

import (
	"os"

	"charm.land/lipgloss/v2"
)

// probeBackground reports whether the background is dark. Platforms without
// the unix select implementation need no single-try variant of their own:
// on Windows lipgloss queries the console once.
func probeBackground(f *os.File) bool {
	return lipgloss.HasDarkBackground(f, f)
}
