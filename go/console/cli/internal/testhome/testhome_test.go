package testhome_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/kit/go/console/cli/internal/testhome"
)

// TestIsolatePointsEveryPerUserDirAtOneTempHome pins what "isolated"
// means: HOME and every per-user XDG base sit under one fresh dir that
// is neither the process's own home nor another test's.
func TestIsolatePointsEveryPerUserDirAtOneTempHome(t *testing.T) {
	before := os.Getenv("HOME")
	testhome.Isolate(t)
	home := os.Getenv("HOME")
	if home == "" || home == before {
		t.Fatalf("HOME = %q, want a fresh dir (was %q)", home, before)
	}
	if !strings.HasPrefix(home, os.TempDir()) && !strings.Contains(home, "Test") {
		t.Fatalf("HOME = %q is not a test temp dir", home)
	}
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		v := os.Getenv(k)
		if rel, err := filepath.Rel(home, v); err != nil || strings.HasPrefix(rel, "..") || rel == "." {
			t.Errorf("%s = %q, want a dir under %q", k, v, home)
		}
	}
}
