package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildServed compiles this fixture into a real binary, so a stdio
// session runs against a process's actual standard streams and its
// actual exit code, not an in-process stand-in.
func buildServed(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a binary")
	}
	bin := filepath.Join(t.TempDir(), "served")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := build.CombinedOutput()
	require.NoError(t, err, "go build: %s", out)
	return bin
}

// isolatedEnv keeps the child away from the operator's configuration
// and state.
func isolatedEnv(t *testing.T) []string {
	t.Helper()
	home := t.TempDir()
	return append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_DATA_HOME="+filepath.Join(home, ".data"),
		"XDG_STATE_HOME="+filepath.Join(home, ".state"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"XDG_RUNTIME_DIR="+filepath.Join(home, ".run"),
	)
}

// TestMCPOverStdioFromABuiltBinary is what a desktop host does: spawn
// `served serve mcp --stdio`, speak MCP on its stdin and stdout, and
// close its stdin to end the session. The SDK client rejects any line
// on stdout that is not a protocol message, so the session working at
// all is the proof that stdout carried nothing else. Over it, `item.add`
// takes its positional from args, `item.sync` runs on the spawn's
// trust, `item.tag` runs once the host's user approves, closing stdin
// exits 0, and the lifecycle trace is on stderr.
func TestMCPOverStdioFromABuiltBinary(t *testing.T) {
	bin := buildServed(t)

	cmd := exec.Command(bin, "serve", "mcp", "--stdio")
	cmd.Env = isolatedEnv(t)
	var stderr safeBuffer
	cmd.Stderr = &stderr

	asked := 0
	client := mcp.NewClient(&mcp.Implementation{Name: "desktop-host", Version: "0"}, &mcp.ClientOptions{
		ElicitationHandler: func(ctx context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			asked++
			return accept(ctx, req)
		},
	})
	sess, err := client.Connect(t.Context(), &mcp.CommandTransport{Command: cmd, TerminateDuration: 10 * time.Second}, nil)
	require.NoError(t, err, stderr.String())

	got := mcpTools(t, sess)
	assert.Subset(t, got, []string{"item.list", "item.add", "item.tag", "item.sync"})
	assert.NotContains(t, got, "item.purge")

	// A positional argument travels in the "args" array.
	text, _, isErr := mcpCall(t, sess, "item.add", map[string]any{"args": []any{"washer"}})
	assert.False(t, isErr, text)
	assert.Equal(t, "added washer\n", text)

	_, data, isErr := mcpCall(t, sess, "item.list", nil)
	require.False(t, isErr)
	assert.Contains(t, toJSON(t, data), `"nut"`)
	assert.Contains(t, toJSON(t, data), `"washer"`)

	// Auth-required runs on the spawn's trust over stdio.
	text, _, isErr = mcpCall(t, sess, "item.sync", nil)
	assert.False(t, isErr, text)
	assert.Equal(t, "synced 3 items\n", text)

	// Confirmation-required runs once the host's user approves.
	text, _, isErr = mcpCall(t, sess, "item.tag", map[string]any{"name": "nut"})
	assert.False(t, isErr, text)
	assert.Equal(t, "tagged nut\n", text)
	assert.Equal(t, 1, asked)

	// Closing stdin ends the session: a clean stop, well inside the
	// grace period before the transport would signal.
	start := time.Now()
	require.NoError(t, sess.Close())
	assert.Less(t, time.Since(start), 5*time.Second, "end of input stops the server")
	require.NotNil(t, cmd.ProcessState)
	assert.Equal(t, 0, cmd.ProcessState.ExitCode(), stderr.String())

	log := stderr.String()
	assert.Contains(t, log, "ready_reported", "the lifecycle trace goes to stderr")
	assert.Contains(t, log, "service=mcp")
}

// TestMCPStdioRefusalsExitWithTheContractCodes pins the built binary's
// exit codes for the stdio transport's configuration failure: --stdio
// with --mcp-addr exits 2.
func TestMCPStdioRefusalsExitWithTheContractCodes(t *testing.T) {
	bin := buildServed(t)

	cmd := exec.Command(bin, "serve", "mcp", "--stdio", "--mcp-addr", "127.0.0.1:0")
	cmd.Env = isolatedEnv(t)
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr, "%s", out)
	assert.Equal(t, 2, exitErr.ExitCode(), "%s", out)
	assert.Contains(t, string(out), "--mcp-addr")
}
