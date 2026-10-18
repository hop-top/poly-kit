// Package testhome isolates a test from the developer's own home
// directory: the per-user config, data, state and cache a kit root
// reads, and that `serve` layers its services.* keys from. The tests
// of go/console/cli and of the service packages under it share it, so
// there is one definition of "isolated".
package testhome

import (
	"path/filepath"
	"strings"
	"testing"
)

// Isolate points HOME and every per-user XDG base directory at a fresh
// temp dir for the rest of t. It uses t.Setenv, so t must not be
// parallel.
//
// The system config file (/etc/<tool>/config.yaml) is not redirectable
// by environment; a test tool's name keeps it absent.
func Isolate(t testing.TB) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(k, filepath.Join(home, strings.ToLower(k)))
	}
}
