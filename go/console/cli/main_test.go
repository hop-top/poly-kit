//go:build !parity

package cli_test

import (
	"fmt"
	"os"
	"testing"
)

// TestMain gives the test binary a runtime directory of its own (the
// parity suite's TestMain does the same under its tag). Every
// `service start` claims a control socket there, and one claimed by
// another package's test binary, or by a tool the user runs, refuses
// the start.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "kr")
	if err != nil {
		fmt.Fprintln(os.Stderr, "runtime dir:", err)
		os.Exit(1)
	}
	if err := os.Setenv("XDG_RUNTIME_DIR", dir); err != nil {
		fmt.Fprintln(os.Stderr, "runtime dir:", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
