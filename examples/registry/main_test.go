package main

import (
	"os"
	"path/filepath"
	"testing"

	"hop.top/kit/internal/testsandbox"
)

// TestMain runs the package in a per-binary sandbox: a root built with
// cli.WithIdentity and peers.With writes an identity key and a peer
// registry under the per-user data dir, which must never be the
// developer's own.
func TestMain(m *testing.M) { os.Exit(testsandbox.Main(m)) }

func TestSandbox_IdentityAndPeersStayOutOfRealHome(t *testing.T) {
	data := os.Getenv("XDG_DATA_HOME")
	testsandbox.RequireSandboxed(t,
		filepath.Join(data, "kit", "identity"),
		filepath.Join(data, "kit", "peers", "peers.db"))
}
