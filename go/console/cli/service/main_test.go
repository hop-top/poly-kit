package service

import (
	"os"
	"testing"

	"hop.top/kit/internal/testsandbox"
)

// TestMain runs the package in a per-binary sandbox: every `serve` and
// `service start` claims a control socket, and every root reads and
// writes per-user config, data and state. Without it they land in the
// developer's real runtime dir and home, and two runs at once refuse
// each other's control socket.
func TestMain(m *testing.M) { os.Exit(testsandbox.Main(m)) }
