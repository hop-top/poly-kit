package record_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/conformance/harness/record"
	"hop.top/kit/go/console/output"
)

const stubScript = `#!/bin/sh
case "$1" in
  status)
    printf '{"state":"ok"}\n'
    ;;
  *)
    echo "unknown: $1" >&2
    exit 2
    ;;
esac
exit 0
`

const storyDoc = `schema_version: "1"
story_id: stub.conformance.status-json
title: Status in JSON
binary: stubcli
intent: |
  Read status as JSON.
steps:
  - id: status
    intent: read status
    invoke: ["stubcli", "status", "--format", "json"]
    capture: [exit_code, stdout]
`

const scenarioTmpl = `schema_version: "1"
scenario_id: status-json
binary: stubcli
factor_coverage: [3, 11]
tier: 3
story_ref:
  story_id: stub.conformance.status-json
  story_path: %s
  content_hash: "sha256:%s"
steps:
  - id: status
    invoke: ["stubcli", "status", "--format", "json"]
    capture: [exit_code, stdout]
assertions:
  - id: status-exits-zero
    kind: exit_code_equals
    on: status
    factor: 11
    value: 0
`

// fixture materializes a stub binary, story, and scenario into a
// library-shaped tree and returns (scenarioPath, storyPath, binPath).
func fixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()

	bin := filepath.Join(root, "stubcli")
	require.NoError(t, os.WriteFile(bin, []byte(stubScript), 0o755))

	storyDir := filepath.Join(root, "stories")
	require.NoError(t, os.MkdirAll(storyDir, 0o755))
	storyPath := filepath.Join(storyDir, "status-json.yaml")
	require.NoError(t, os.WriteFile(storyPath, []byte(storyDoc), 0o644))

	sum := sha256.Sum256([]byte(storyDoc))
	scDir := filepath.Join(root, "scenarios", "stub", "status-json", "1.0.0")
	require.NoError(t, os.MkdirAll(scDir, 0o755))
	scPath := filepath.Join(scDir, "scenario.yaml")
	raw := fmt.Sprintf(scenarioTmpl, "stories/status-json.yaml", hex.EncodeToString(sum[:]))
	require.NoError(t, os.WriteFile(scPath, []byte(raw), 0o644))

	return scPath, storyPath, bin
}

func runLeaf(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := record.Cmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func exitCodeOf(t *testing.T, err error) int {
	t.Helper()
	var oe *output.Error
	require.True(t, errors.As(err, &oe), "leaf errors must be *output.Error envelopes, got %T: %v", err, err)
	return oe.ExitCode
}

func TestGroup_Surface(t *testing.T) {
	g := record.Group()
	assert.Equal(t, "harness", g.Name())
	assert.NotEmpty(t, g.Short)
	assert.True(t, cli.IsHierarchical(g), "harness group must carry kit/hierarchical for depth-3 leaves")
	names := []string{}
	for _, c := range g.Commands() {
		names = append(names, c.Name())
	}
	assert.Contains(t, names, "record")
}

// TestRecord_ExitCodesUseSharedTaxonomy pins the leaf to kit's shared
// exit-code bands. The leaf previously invented its own numbering
// (3 usage, 4 io, 5 story), which collided with NOT_FOUND, CONFLICT
// and UNAUTHORIZED for anything branching on the code alone.
func TestRecord_ExitCodesUseSharedTaxonomy(t *testing.T) {
	scPath, storyPath, bin := fixture(t)

	t.Run("usage is 2", func(t *testing.T) {
		_, err := runLeaf(t)
		require.Error(t, err)
		assert.Equal(t, 2, exitCodeOf(t, err))
	})

	t.Run("io is transient 6", func(t *testing.T) {
		_, err := runLeaf(t, "--scenario", scPath,
			"--binary", "/nonexistent/bin",
			"--out", filepath.Join(t.TempDir(), "c"))
		require.Error(t, err)
		assert.Equal(t, output.ExitTransient, exitCodeOf(t, err))
		assert.Equal(t, 6, exitCodeOf(t, err))
	})

	t.Run("story error never claims the auth slot", func(t *testing.T) {
		require.NoError(t, os.Remove(storyPath))
		_, err := runLeaf(t, "--scenario", scPath, "--binary", bin,
			"--out", filepath.Join(t.TempDir(), "c"))
		require.Error(t, err)
		assert.NotEqual(t, 5, exitCodeOf(t, err),
			"exit 5 is the shared UNAUTHORIZED slot")
		assert.Equal(t, 2, exitCodeOf(t, err))
	})
}

func TestRecord_MissingFlagsIsUsage(t *testing.T) {
	_, err := runLeaf(t)
	require.Error(t, err)
	assert.Equal(t, 2, exitCodeOf(t, err))
	assert.Contains(t, err.Error(), "--scenario")
}

func TestRecord_EndToEndWithDerivedRef(t *testing.T) {
	scPath, _, bin := fixture(t)
	out := filepath.Join(t.TempDir(), "cassette")

	stdout, err := runLeaf(
		t,
		"--scenario", scPath,
		"--binary", bin,
		"--out", out,
		"--format", "json",
	)
	require.NoError(t, err)

	// Report JSON parses and carries the derived, namespace-qualified
	// scenario ref plus the observed step results.
	var rep struct {
		ScenarioID string `json:"scenario_id"`
		StoryHash  string `json:"story_hash"`
		Steps      []struct {
			ID       string `json:"id"`
			ExitCode int    `json:"exit_code"`
		} `json:"steps"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &rep), "report must be JSON: %s", stdout)
	assert.Equal(t, "stub/status-json", rep.ScenarioID)
	require.Len(t, rep.Steps, 1)
	assert.Equal(t, "status", rep.Steps[0].ID)
	assert.Equal(t, 0, rep.Steps[0].ExitCode)

	// Layout on disk: manifest + story + step captures.
	for _, rel := range []string{
		"manifest.yaml", "story.yaml",
		"steps/status/result.json", "steps/status/stdout.txt", "steps/status/stderr.txt",
	} {
		_, err := os.Stat(filepath.Join(out, rel))
		assert.NoErrorf(t, err, "expected %s in the recorded cassette", rel)
	}
	manifest, err := os.ReadFile(filepath.Join(out, "manifest.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(manifest), "scenario_id: stub/status-json")
	assert.Contains(t, string(manifest), "scenario_version: 1.0.0")
	assert.Contains(t, string(manifest), "recorder: xrr")
}

func TestRecord_ScenarioRefOverride(t *testing.T) {
	scPath, _, bin := fixture(t)
	out := filepath.Join(t.TempDir(), "cassette")
	_, err := runLeaf(
		t,
		"--scenario", scPath, "--binary", bin, "--out", out,
		"--scenario-ref", "acme/status-json@2.0.0",
		"--format", "json",
	)
	require.NoError(t, err)
	manifest, err := os.ReadFile(filepath.Join(out, "manifest.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(manifest), "scenario_id: acme/status-json")
	assert.Contains(t, string(manifest), "scenario_version: 2.0.0")
}

func TestRecord_MalformedScenarioRefIsUsage(t *testing.T) {
	scPath, _, bin := fixture(t)
	_, err := runLeaf(
		t,
		"--scenario", scPath, "--binary", bin, "--out", filepath.Join(t.TempDir(), "c"),
		"--scenario-ref", "no-namespace",
	)
	require.Error(t, err)
	assert.Equal(t, 2, exitCodeOf(t, err))
}

func TestRecord_InvalidScenarioExits2(t *testing.T) {
	dir := t.TempDir()
	scPath := filepath.Join(dir, "scenario.yaml")
	// Parses, but fails validation (no steps/assertions/factors).
	require.NoError(t, os.WriteFile(scPath, []byte("schema_version: \"1\"\nscenario_id: broken\n"), 0o644))
	_, err := runLeaf(t, "--scenario", scPath, "--binary", "/bin/sh", "--out", filepath.Join(dir, "out"))
	require.Error(t, err)
	assert.Equal(t, 2, exitCodeOf(t, err))
}

func TestRecord_UnsupportedSchemaExits1(t *testing.T) {
	dir := t.TempDir()
	scPath := filepath.Join(dir, "scenario.yaml")
	require.NoError(t, os.WriteFile(scPath, []byte("schema_version: \"99\"\n"), 0o644))
	_, err := runLeaf(t, "--scenario", scPath, "--binary", "/bin/sh", "--out", filepath.Join(dir, "out"))
	require.Error(t, err)
	assert.Equal(t, 1, exitCodeOf(t, err))
}

func TestRecord_MissingStoryIsUsage(t *testing.T) {
	scPath, storyPath, bin := fixture(t)
	require.NoError(t, os.Remove(storyPath))
	_, err := runLeaf(t, "--scenario", scPath, "--binary", bin, "--out", filepath.Join(t.TempDir(), "c"))
	require.Error(t, err)
	assert.Equal(t, 2, exitCodeOf(t, err))
}

func TestRecord_StoryHashMismatchIsUsage(t *testing.T) {
	scPath, storyPath, bin := fixture(t)
	require.NoError(t, os.WriteFile(storyPath, []byte(storyDoc+"# drift\n"), 0o644))
	_, err := runLeaf(t, "--scenario", scPath, "--binary", bin, "--out", filepath.Join(t.TempDir(), "c"))
	require.Error(t, err)
	assert.Equal(t, 2, exitCodeOf(t, err))
	assert.Contains(t, err.Error(), "hash mismatch")
}

func TestRecord_MissingBinaryIsTransient(t *testing.T) {
	scPath, _, _ := fixture(t)
	_, err := runLeaf(t, "--scenario", scPath, "--binary", "/nonexistent/bin", "--out", filepath.Join(t.TempDir(), "c"))
	require.Error(t, err)
	assert.Equal(t, output.ExitTransient, exitCodeOf(t, err))
}

func TestRecord_NonEmptyOutRequiresForce(t *testing.T) {
	scPath, _, bin := fixture(t)
	out := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(out, "unrelated.txt"), []byte("x"), 0o644))

	// Without --force: refused.
	_, err := runLeaf(t, "--scenario", scPath, "--binary", bin, "--out", out)
	require.Error(t, err)
	assert.Equal(t, 2, exitCodeOf(t, err))

	// With --force but no manifest.yaml: still refused — the dir is
	// not recognizably a prior recording.
	_, err = runLeaf(t, "--scenario", scPath, "--binary", bin, "--out", out, "--force")
	require.Error(t, err)
	assert.Equal(t, 2, exitCodeOf(t, err))
	assert.Contains(t, err.Error(), "manifest.yaml")
	_, statErr := os.Stat(filepath.Join(out, "unrelated.txt"))
	assert.NoError(t, statErr, "refused --force must not delete anything")
}

func TestRecord_ForceReplacesPriorRecording(t *testing.T) {
	scPath, _, bin := fixture(t)
	out := filepath.Join(t.TempDir(), "cassette")
	_, err := runLeaf(t, "--scenario", scPath, "--binary", bin, "--out", out, "--format", "json")
	require.NoError(t, err)

	// Second run without --force: refused.
	_, err = runLeaf(t, "--scenario", scPath, "--binary", bin, "--out", out)
	require.Error(t, err)
	assert.Equal(t, 2, exitCodeOf(t, err))

	// With --force: re-recorded.
	_, err = runLeaf(t, "--scenario", scPath, "--binary", bin, "--out", out, "--force", "--format", "json")
	require.NoError(t, err)
	_, statErr := os.Stat(filepath.Join(out, "manifest.yaml"))
	assert.NoError(t, statErr)
}

func TestRecord_HumanReportRenders(t *testing.T) {
	scPath, _, bin := fixture(t)
	out := filepath.Join(t.TempDir(), "cassette")
	stdout, err := runLeaf(t, "--scenario", scPath, "--binary", bin, "--out", out, "--format", "human")
	require.NoError(t, err)
	assert.Contains(t, stdout, "recorded: stub/status-json")
	assert.Contains(t, stdout, "exit=0")
}

func TestCmd_Annotations(t *testing.T) {
	cmd := record.Cmd()
	se, ok := cli.GetSideEffect(cmd)
	require.True(t, ok, "record leaf must declare kit/side-effect")
	assert.Equal(t, cli.SideEffectWrite, se)
	id, ok := cli.GetIdempotency(cmd)
	require.True(t, ok, "record leaf must declare kit/idempotent")
	assert.Equal(t, cli.IdempotencyNo, id)
	assert.NotEmpty(t, cmd.Short)
	assert.NotEmpty(t, cmd.Long)
	assert.NotEmpty(t, cmd.Example)
}
