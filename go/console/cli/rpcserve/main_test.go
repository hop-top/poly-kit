package rpcserve_test

import (
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/service"
)

// TestMain gives the test binary a runtime directory of its own. Every
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

// ownScope gives a test instance a control scope no other instance in
// this binary uses, so tests that run two at once do not refuse each
// other's start.
func ownScope() func(*cli.Root) {
	s := fmt.Sprintf("t%d", scopeSeq.Add(1))
	return service.WithServiceControlScope(func(*cobra.Command) (string, error) { return s, nil })
}

var scopeSeq atomic.Int64
