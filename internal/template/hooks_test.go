// Black-box tests for template.Run lifecycle hook executor (spec §16).
//
// Fixtures live in testdata/hooks/ and are tiny POSIX sh scripts
// invoked via /bin/sh — no execute bit required.
package template_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/internal/template"
)

func hookCtx() template.HookContext {
	return template.HookContext{
		Vars:      map[string]any{"Name": "demo"},
		Mode:      "bootstrap",
		Tier:      1,
		TargetDir: "/tmp/target",
	}
}

func testdataRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("testdata/hooks")
	require.NoError(t, err)
	return root
}

func TestRun_Success(t *testing.T) {
	var out bytes.Buffer
	err := template.Run(context.Background(), []string{"ok.sh"},
		testdataRoot(t), hookCtx(), &out)
	require.NoError(t, err)
	assert.Contains(t, out.String(), "[hook:ok.sh] ok")
}

func TestRun_Failure(t *testing.T) {
	var out bytes.Buffer
	err := template.Run(context.Background(), []string{"fail.sh"},
		testdataRoot(t), hookCtx(), &out)
	require.Error(t, err)
	assert.True(t, template.IsHookFailed(err), "expected IsHookFailed(err) true")

	var hookErr *template.HookFailedError
	require.True(t, errors.As(err, &hookErr), "expected *HookFailedError")
	assert.Equal(t, 2, hookErr.ExitCode)
	assert.Equal(t, "fail.sh", hookErr.Name)
}

func TestRun_StdinJSON(t *testing.T) {
	var out bytes.Buffer
	err := template.Run(context.Background(), []string{"echo-stdin.sh"},
		testdataRoot(t), hookCtx(), &out)
	require.NoError(t, err)

	got := out.String()
	for _, key := range []string{`"vars"`, `"mode"`, `"tier"`, `"target_dir"`} {
		assert.Contains(t, got, key, "expected stdin JSON key %s in forwarded output", key)
	}
}

func TestRun_StopsOnFailure(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ok2-ran")
	t.Setenv("MARKER_FILE", marker)

	var out bytes.Buffer
	err := template.Run(context.Background(),
		[]string{"ok.sh", "fail.sh", "ok2.sh"},
		testdataRoot(t), hookCtx(), &out)
	require.Error(t, err)
	assert.True(t, template.IsHookFailed(err))

	_, statErr := os.Stat(marker)
	assert.True(t, os.IsNotExist(statErr),
		"ok2.sh must not have executed; marker exists at %s (statErr=%v)",
		marker, statErr)
}

func TestRun_MissingScript(t *testing.T) {
	var out bytes.Buffer
	err := template.Run(context.Background(), []string{"nonexistent.sh"},
		t.TempDir(), hookCtx(), &out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent.sh",
		"error must mention the offending script path")
}
