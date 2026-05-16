package conformance_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/conformance"
)

// helper: run the verify-stories leaf via the parent Cmd() with args.
func runVerifyStories(t *testing.T, args ...string) (stdout, stderr *bytes.Buffer, err error) {
	t.Helper()
	root := conformance.Cmd()
	root.SetArgs(append([]string{"verify-stories"}, args...))
	stdout = &bytes.Buffer{}
	stderr = &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(stderr)
	err = root.Execute()
	return
}

func writeFile(t *testing.T, dir, name, src string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(src), 0644))
	return p
}

const goodCLIStory = `schema_version: "1"
story_id: spaced.launch.dry-run
title: Preview a launch
binary: spaced
intent: An operator wants to preview a spaced launch before committing to a real run end to end.
steps:
  - id: preview
    invoke: ["spaced", "launch", "--dry-run"]
    capture: [exit_code, stdout]
`

func TestVerifyStoriesCLIClean(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ok.yaml", goodCLIStory)
	stdout, _, err := runVerifyStories(t, "--paths", dir)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "1 file(s) scanned, 0 findings")
}

func TestVerifyStoriesCLIScenarioShapeFails(t *testing.T) {
	dir := t.TempDir()
	bad := goodCLIStory + "\nscenario_id: oops.scenario.x\n"
	writeFile(t, dir, "bad.yaml", bad)
	_, _, err := runVerifyStories(t, "--paths", dir)
	require.Error(t, err)
	// errors.Is matches the leak-detected sentinel since
	// verify-stories piggybacks on it for the exit envelope.
	assert.True(t, errors.Is(err, conformance.ErrLeakDetected) ||
		errors.Is(err, conformance.ErrUsage) ||
		errors.Is(err, conformance.ErrConfig),
		"expected a typed sentinel; got %v", err)
}

func TestVerifyStoriesCLIJSONOutput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ok.yaml", goodCLIStory)
	stdout, _, err := runVerifyStories(t, "--paths", dir, "--format=json")
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
	assert.Equal(t, "verify-stories", got["tool"])
	assert.EqualValues(t, 1, got["scanned_files"])
}

func TestVerifyStoriesCLIQuietOnClean(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ok.yaml", goodCLIStory)
	stdout, _, err := runVerifyStories(t, "--paths", dir, "--quiet-on-clean")
	require.NoError(t, err)
	assert.Empty(t, stdout.String(), "quiet-on-clean should suppress all output on clean runs")
}

func TestVerifyStoriesCLIUnknownFormat(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ok.yaml", goodCLIStory)
	_, _, err := runVerifyStories(t, "--paths", dir, "--format=xml")
	require.Error(t, err)
	assert.True(t, errors.Is(err, conformance.ErrUsage))
}

func TestVerifyStoriesCLIMissingDefaultDirectory(t *testing.T) {
	// In a tempdir with no e2e/stories/, the leaf should
	// ConfigError, not panic / silently exit 0.
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	require.NoError(t, os.Chdir(dir))
	_, _, err := runVerifyStories(t)
	require.Error(t, err)
	assert.True(t, errors.Is(err, conformance.ErrConfig))
}

func TestVerifyStoriesCLIForbiddenMetadataKey(t *testing.T) {
	dir := t.TempDir()
	src := goodCLIStory + `metadata:
  scenario_id: oops.scenario.x
`
	writeFile(t, dir, "bad.yaml", src)
	stdout, _, err := runVerifyStories(t, "--paths", dir)
	require.Error(t, err)
	assert.Contains(t, stdout.String(), "forbidden-metadata-key")
}
