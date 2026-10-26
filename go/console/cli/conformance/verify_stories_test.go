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

// A --paths=. default (12fcc's post-PR#186 CI gate template) must not
// walk into node_modules/ or vendor/: unrelated YAML sitting there
// (e.g. pnpm's own metadata files, vendored deps) is not a story and
// must never surface as a parse-failed finding. walkYAMLs is the
// walker actually reachable via --paths=.; before the fix it pruned
// only dot-directories, so both trees below were walked and choked on
// invalid YAML that has nothing to do with kit stories.
func TestVerifyStoriesCLIPrunesNodeModulesAndVendor(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ok.yaml", goodCLIStory)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "node_modules", "some-pkg"), 0o755))
	writeFile(t, dir, filepath.Join("node_modules", "some-pkg", "bad.yaml"), "not: valid: yaml: [\n")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "vendor", "some-go-dep"), 0o755))
	writeFile(t, dir, filepath.Join("vendor", "some-go-dep", "bad.yaml"), "broken: [yaml\n")

	stdout, _, err := runVerifyStories(t, "--paths", dir, "--format=json")
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
	assert.EqualValues(t, 1, got["scanned_files"], "node_modules/ and vendor/ must be pruned entirely, not walked-and-tolerated")
	findings, _ := got["findings"].([]any)
	assert.Empty(t, findings, "expected no findings; got %v", findings)
}
