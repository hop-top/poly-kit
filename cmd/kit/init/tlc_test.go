// T-1062: runTLCInit — best-effort tlc init wiring. Two scenarios under
// test: (1) tlc absent from PATH → silent no-op (skipped=true, nil); (2)
// tlc present but exits non-zero → wrapped error surfaces.
//
// We simulate "tlc missing" via t.Setenv("PATH", "") and "tlc failing"
// by writing a one-shot stub script that exits with status 1, then
// pointing PATH at the dir containing it.
package kitinit

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunTLCInit_MissingBinary(t *testing.T) {
	// Empty PATH simulates "tlc not installed" without touching the
	// real shell environment of the test host.
	t.Setenv("PATH", "")
	dir := t.TempDir()

	skipped, err := runTLCInit(context.Background(), dir)
	require.NoError(t, err, "missing tlc must NOT surface as an error")
	assert.True(t, skipped, "missing tlc must report skipped=true")
}

func TestRunTLCInit_RuntimeFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-stub tlc not portable to Windows")
	}

	// Write a stub `tlc` that exits 1 with a diagnostic on stderr; the
	// stub also accepts any args so `tlc init` doesn't choke on argv
	// validation.
	stubDir := t.TempDir()
	stubPath := filepath.Join(stubDir, "tlc")
	stub := "#!/bin/sh\necho 'simulated tlc failure' >&2\nexit 1\n"
	require.NoError(t, os.WriteFile(stubPath, []byte(stub), 0o755))

	t.Setenv("PATH", stubDir)
	dir := t.TempDir()

	skipped, err := runTLCInit(context.Background(), dir)
	require.Error(t, err, "tlc runtime failure must surface as error")
	assert.False(t, skipped, "runtime failures must not report skipped")
	assert.Contains(t, err.Error(), "tlc init",
		"error must be wrapped with the 'tlc init' prefix")
	assert.Contains(t, err.Error(), "simulated tlc failure",
		"error must include the stub's stderr for diagnostics")
}

func TestRunTLCInit_ExistingScope(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-stub tlc not portable to Windows")
	}

	// Even with a working tlc on PATH, a pre-existing .tlc directory
	// should short-circuit to a skipped no-op (we don't pass --force
	// because that would clobber the user's config).
	stubDir := t.TempDir()
	stubPath := filepath.Join(stubDir, "tlc")
	stub := "#!/bin/sh\necho 'should not be invoked' >&2\nexit 1\n"
	require.NoError(t, os.WriteFile(stubPath, []byte(stub), 0o755))
	t.Setenv("PATH", stubDir)

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".tlc"), 0o750))

	skipped, err := runTLCInit(context.Background(), dir)
	require.NoError(t, err, "existing .tlc scope must not invoke tlc")
	assert.True(t, skipped, "existing .tlc scope must report skipped")
}

func TestRunTLCInit_Success(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-stub tlc not portable to Windows")
	}

	// Stub tlc that exits 0; verifies the happy path returns
	// (skipped=false, nil).
	stubDir := t.TempDir()
	stubPath := filepath.Join(stubDir, "tlc")
	stub := "#!/bin/sh\nexit 0\n"
	require.NoError(t, os.WriteFile(stubPath, []byte(stub), 0o755))

	t.Setenv("PATH", stubDir)
	dir := t.TempDir()

	skipped, err := runTLCInit(context.Background(), dir)
	require.NoError(t, err)
	assert.False(t, skipped, "tlc on PATH must not report skipped")
}
