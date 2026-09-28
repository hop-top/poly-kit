package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/socket"
)

// verifyChain runs `served audit verify` against config and returns its
// stdout and process exit code.
func verifyChain(t *testing.T, config map[string]any, args ...string) (string, int) {
	t.Helper()
	root := newRoot(options{config: config})
	var out bytes.Buffer
	root.Cmd.SetOut(&out)
	root.Cmd.SetErr(&bytes.Buffer{})
	root.SetArgs(append([]string{"audit", "verify"}, args...))
	if err := root.Execute(t.Context()); err != nil {
		return out.String(), exitCode(err)
	}
	return out.String(), 0
}

// An operator names a chain in configuration; every served call lands
// in it, `audit verify` passes on the untouched file, and an edited
// record exits 71 naming where the chain breaks.
func TestAuditChainRecordsServedCallsAndVerifies(t *testing.T) {
	chain := filepath.Join(t.TempDir(), "audit.chain")
	config := map[string]any{"services.all.audit.sinks": []any{map[string]any{"type": "chain", "path": chain}}}

	path := shortSocketPath(t)
	run := startServe(t, options{config: config}, "socket", "--socket", path)
	run.waitReady(t, "socket")
	for _, name := range []string{"washer", "spring"} {
		resp := callSocket(t, path, socket.Request{Path: []string{"item", "add"}, Args: []string{name}})
		require.True(t, resp.Ok, "%+v", resp.Error)
	}
	resp := callSocket(t, path, socket.Request{Path: []string{"item", "purge"}})
	require.False(t, resp.Ok, "a refusal is audited too")
	require.NoError(t, run.stop(t))

	out, code := verifyChain(t, config, "--format", "json")
	require.Equal(t, 0, code, out)
	assert.Contains(t, out, `"records": 3`)

	raw, err := os.ReadFile(chain)
	require.NoError(t, err)
	edited := strings.Replace(string(raw), `"args":["spring"]`, `"args":["bolt"]`, 1)
	require.NotEqual(t, string(raw), edited)
	require.NoError(t, os.WriteFile(chain, []byte(edited), 0o600))

	out, code = verifyChain(t, config)
	assert.Equal(t, 71, code, out)
	assert.Contains(t, out, "tampered")
}
