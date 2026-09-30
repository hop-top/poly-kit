package llm_test

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points the XDG cache at a throwaway directory for the whole
// package: key resolution and Resolve read the aim catalog cache, and a
// developer's real cache must neither satisfy nor poison an assertion.
// Tests that want a catalog install a fixture one (useCatalog).
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "llm-test-cache-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.Setenv("XDG_CACHE_HOME", dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
