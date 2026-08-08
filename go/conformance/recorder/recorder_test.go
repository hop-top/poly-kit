package recorder_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/conformance/client"
	"hop.top/kit/go/conformance/recorder"
	"hop.top/kit/go/conformance/scenario"
	"hop.top/kit/go/conformance/svc"
)

// stubScript is a minimal POSIX-sh target binary with fully known
// behavior: `greet --format json` prints one JSON line on stdout and
// exits 0; `explode` echoes stdin + an env var to stderr, prints
// "boom" on stdout, and exits 3.
const stubScript = `#!/bin/sh
case "$1" in
  greet)
    printf '{"greeting":"hello","mode":"%s"}\n' "$3"
    ;;
  explode)
    read -r line
    echo "boom"
    echo "got:$line" >&2
    echo "mark:$STUB_MARK" >&2
    exit 3
    ;;
  *)
    echo "unknown: $1" >&2
    exit 2
    ;;
esac
exit 0
`

const storyDoc = `schema_version: "1"
story_id: stub.conformance.echo-basic
title: Echo basics
binary: stubcli
intent: |
  Exercise the recorder against a stub with known output.
steps:
  - id: greet
    intent: greet in JSON mode
    invoke: ["stubcli", "greet", "--format", "json"]
    capture: [exit_code, stdout, stderr]
  - id: explode
    intent: fail loudly
    invoke: ["stubcli", "explode"]
    capture: [exit_code, stdout, stderr]
`

// writeStub materializes the stub binary into dir and returns its
// path.
func writeStub(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "stubcli")
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

const scenarioTmpl = `schema_version: "1"
scenario_id: echo-basic
binary: stubcli
factor_coverage: [3, 11]
tier: 3
story_ref:
  story_id: stub.conformance.echo-basic
  story_path: story.yaml
  content_hash: "sha256:%s"
steps:
  - id: greet
    invoke: ["stubcli", "greet", "--format", "json"]
    capture: [exit_code, stdout, stderr]
    delay: 5ms
  - id: explode
    invoke: ["stubcli", "explode"]
    capture: [exit_code, stdout, stderr]
    env:
      STUB_MARK: xyz
    stdin: "ping\n"
assertions:
  - id: greet-exits-zero
    kind: exit_code_equals
    on: greet
    factor: 11
    value: 0
  - id: greet-json-field
    kind: output_field_equals
    on: greet
    factor: 3
    path: greeting
    value: hello
`

// parseStubScenario renders scenarioTmpl against the story bytes'
// real hash and returns the parsed + validated document.
func parseStubScenario(t *testing.T, story []byte) *scenario.Scenario {
	t.Helper()
	sum := sha256.Sum256(story)
	raw := fmt.Sprintf(scenarioTmpl, hex.EncodeToString(sum[:]))
	sc, err := scenario.ParseBytes([]byte(raw), "stub-scenario")
	require.NoError(t, err)
	require.NoError(t, scenario.Validate(sc))
	return sc
}

func runStub(t *testing.T, mutate func(*recorder.Options)) (*recorder.Result, string, error) {
	t.Helper()
	story := []byte(storyDoc)
	sc := parseStubScenario(t, story)
	bin := writeStub(t, stubScript)
	out := filepath.Join(t.TempDir(), "cassette")
	opts := recorder.Options{
		Scenario:        sc,
		StoryBytes:      story,
		OutDir:          out,
		Invoker:         &recorder.ExecInvoker{Path: bin, Dir: t.TempDir()},
		ScenarioID:      "stub/echo-basic",
		ScenarioVersion: "1.0.0",
		BinaryVersion:   "test-sha",
		RecorderVersion: "9.9.9-test",
		Now:             func() time.Time { return time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC) },
	}
	if mutate != nil {
		mutate(&opts)
	}
	res, err := recorder.Run(context.Background(), opts)
	return res, out, err
}

func TestRun_WritesSvcValidManifest(t *testing.T) {
	res, out, err := runStub(t, nil)
	require.NoError(t, err)
	require.NotNil(t, res)

	f, err := os.Open(filepath.Join(out, "manifest.yaml"))
	require.NoError(t, err)
	defer f.Close()
	m, err := svc.LoadManifest(f)
	require.NoError(t, err)
	require.NoError(t, svc.ValidateManifest(m), "recorded manifest must pass svc.ValidateManifest")

	assert.Equal(t, "1", m.SchemaVersion)
	assert.Equal(t, "stubcli", m.Binary)
	assert.Equal(t, "test-sha", m.BinaryVersion)
	assert.Equal(t, "xrr", m.Recorder)
	assert.Equal(t, "9.9.9-test", m.RecorderVersion)
	assert.Equal(t, time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC), m.RecordedAt.UTC())
	assert.Equal(t, "stub/echo-basic", m.ScenarioID)
	assert.Equal(t, "1.0.0", m.ScenarioVersion)
	assert.Equal(t, "stub.conformance.echo-basic", m.StoryRef.StoryID)
	assert.Equal(t, "story.yaml", m.StoryRef.StoryPath)

	// content_hash matches both the scenario's declared hash and the
	// story.yaml bytes actually written to the out dir.
	storyOut, err := os.ReadFile(filepath.Join(out, "story.yaml"))
	require.NoError(t, err)
	assert.Equal(t, []byte(storyDoc), storyOut, "story.yaml must be a byte-exact copy")
	sum := sha256.Sum256(storyOut)
	assert.Equal(t, "sha256:"+hex.EncodeToString(sum[:]), m.StoryRef.ContentHash)

	require.Len(t, m.Steps, 2)
	assert.Equal(t, "greet", m.Steps[0].ID)
	assert.Equal(t, "steps/greet/cassette", m.Steps[0].CassetteDir)
	assert.Equal(t, "steps/greet", m.Steps[0].Captures)
	assert.Equal(t, "explode", m.Steps[1].ID)
}

func TestRun_CapturesFaithful(t *testing.T) {
	res, out, err := runStub(t, nil)
	require.NoError(t, err)
	require.Len(t, res.Steps, 2)

	// greet: exact stdout bytes from a real subprocess run.
	stdout, err := os.ReadFile(filepath.Join(out, "steps", "greet", "stdout.txt"))
	require.NoError(t, err)
	assert.Equal(t, "{\"greeting\":\"hello\",\"mode\":\"json\"}\n", string(stdout))
	stderr, err := os.ReadFile(filepath.Join(out, "steps", "greet", "stderr.txt"))
	require.NoError(t, err)
	assert.Empty(t, string(stderr))

	var greetRes struct {
		ExitCode   int   `json:"exit_code"`
		DurationMS int64 `json:"duration_ms"`
	}
	raw, err := os.ReadFile(filepath.Join(out, "steps", "greet", "result.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &greetRes))
	assert.Equal(t, 0, greetRes.ExitCode)
	assert.GreaterOrEqual(t, greetRes.DurationMS, int64(0))

	// explode: per-step stdin and env must reach the subprocess; the
	// non-zero exit must be recorded, not treated as a run failure.
	stdout, err = os.ReadFile(filepath.Join(out, "steps", "explode", "stdout.txt"))
	require.NoError(t, err)
	assert.Equal(t, "boom\n", string(stdout))
	stderr, err = os.ReadFile(filepath.Join(out, "steps", "explode", "stderr.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(stderr), "got:ping", "step stdin must be piped to the subprocess")
	assert.Contains(t, string(stderr), "mark:xyz", "step env must be applied to the subprocess")
	raw, err = os.ReadFile(filepath.Join(out, "steps", "explode", "result.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &greetRes))
	assert.Equal(t, 3, greetRes.ExitCode)

	assert.Equal(t, 0, res.Steps[0].ExitCode)
	assert.Equal(t, 3, res.Steps[1].ExitCode)
}

func TestRun_StoryHashMismatchFails(t *testing.T) {
	_, out, err := runStub(t, func(o *recorder.Options) {
		o.StoryBytes = []byte(storyDoc + "# tampered\n")
	})
	require.Error(t, err)
	assert.True(t, recorder.IsStoryHashMismatch(err),
		"tampered story bytes must fail the content-hash guard, got: %v", err)
	_, statErr := os.Stat(filepath.Join(out, "manifest.yaml"))
	assert.True(t, os.IsNotExist(statErr), "no manifest may be written on hash mismatch")
}

func TestRun_OutDirMustBeEmpty(t *testing.T) {
	pre := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(pre, "keep.txt"), []byte("x"), 0o644))
	_, _, err := runStub(t, func(o *recorder.Options) { o.OutDir = pre })
	require.Error(t, err)
	assert.True(t, recorder.IsOutDirNotEmpty(err), "non-empty out dir must be refused, got: %v", err)
}

func TestRun_ManifestDeterministic(t *testing.T) {
	_, out1, err := runStub(t, nil)
	require.NoError(t, err)
	_, out2, err := runStub(t, nil)
	require.NoError(t, err)
	m1, err := os.ReadFile(filepath.Join(out1, "manifest.yaml"))
	require.NoError(t, err)
	m2, err := os.ReadFile(filepath.Join(out2, "manifest.yaml"))
	require.NoError(t, err)
	assert.Equal(t, string(m1), string(m2), "fixed clock + recorder version must yield byte-identical manifests")
}

func TestRun_PackedCassetteAcceptedBySvc(t *testing.T) {
	// The full wire proof: pack the recorded dir with the client's
	// deterministic packer and feed it through the svc receiver that
	// guards the upload endpoint.
	_, out, err := runStub(t, nil)
	require.NoError(t, err)

	body, key, err := client.Pack(out, nil, 0)
	require.NoError(t, err)
	defer body.Close()
	assert.True(t, strings.HasPrefix(key, "sha256:"))

	rc := &svc.CassetteReceiver{}
	cas, err := rc.Receive(body)
	require.NoError(t, err, "svc receiver must accept the recorded cassette")
	defer cas.Close()

	require.NoError(t, svc.ValidateManifest(cas.Manifest))
	require.Contains(t, cas.Steps, "greet")
	require.Contains(t, cas.Steps, "explode")
	assert.Equal(t, 0, cas.Steps["greet"].ExitCode)
	assert.Equal(t, 3, cas.Steps["explode"].ExitCode)
	assert.Equal(t, "{\"greeting\":\"hello\",\"mode\":\"json\"}\n", string(cas.Steps["greet"].Stdout))
	assert.Contains(t, string(cas.Steps["explode"].Stderr), "got:ping")
}

func TestExecInvoker_StepTimeout(t *testing.T) {
	story := []byte(storyDoc)
	sc := parseStubScenario(t, story)
	bin := writeStub(t, "#!/bin/sh\nsleep 5\n")
	_, err := recorder.Run(context.Background(), recorder.Options{
		Scenario:        sc,
		StoryBytes:      story,
		OutDir:          filepath.Join(t.TempDir(), "out"),
		Invoker:         &recorder.ExecInvoker{Path: bin, Dir: t.TempDir(), Timeout: 100 * time.Millisecond},
		RecorderVersion: "9.9.9-test",
	})
	require.Error(t, err, "a step exceeding the timeout is a recording failure, not a capture")
}

func TestDeriveRef_FSStoreLayout(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "scenarios", "stub", "echo-basic", "1.0.0")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, "scenario.yaml")

	sc := &scenario.Scenario{ScenarioID: "echo-basic"}
	id, ver := recorder.DeriveRef(path, sc)
	assert.Equal(t, "stub/echo-basic", id)
	assert.Equal(t, "1.0.0", ver)

	// Layout mismatch (id in path != scenario_id) must not invent a ref.
	sc2 := &scenario.Scenario{ScenarioID: "other-id"}
	id, ver = recorder.DeriveRef(path, sc2)
	assert.Equal(t, "other-id", id)
	assert.Empty(t, ver)

	// Non-FSStore path falls back to the bare scenario_id.
	id, ver = recorder.DeriveRef(filepath.Join(root, "scenario.yaml"), sc)
	assert.Equal(t, "echo-basic", id)
	assert.Empty(t, ver)
}

func TestResolveStory_AncestorWalk(t *testing.T) {
	// nerv-shaped tree: scenario at
	//   <root>/scenarios/<ns>/<id>/<ver>/scenario.yaml
	// with story_path "stories/<id>.yaml" resolving at <root>.
	root := t.TempDir()
	scDir := filepath.Join(root, "scenarios", "stub", "echo-basic", "1.0.0")
	require.NoError(t, os.MkdirAll(scDir, 0o755))
	scPath := filepath.Join(scDir, "scenario.yaml")
	require.NoError(t, os.WriteFile(scPath, []byte("placeholder"), 0o644))
	storyDir := filepath.Join(root, "stories")
	require.NoError(t, os.MkdirAll(storyDir, 0o755))
	want := []byte(storyDoc)
	require.NoError(t, os.WriteFile(filepath.Join(storyDir, "echo-basic.yaml"), want, 0o644))

	got, path, err := recorder.ResolveStory(scPath, "stories/echo-basic.yaml", "")
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, filepath.Join(storyDir, "echo-basic.yaml"), path)

	// Explicit path wins over story_path resolution.
	explicit := filepath.Join(t.TempDir(), "explicit.yaml")
	require.NoError(t, os.WriteFile(explicit, []byte("explicit"), 0o644))
	got, path, err = recorder.ResolveStory(scPath, "stories/echo-basic.yaml", explicit)
	require.NoError(t, err)
	assert.Equal(t, []byte("explicit"), got)
	assert.Equal(t, explicit, path)

	// Unresolvable story is a typed failure.
	_, _, err = recorder.ResolveStory(scPath, "stories/missing.yaml", "")
	require.Error(t, err)
	assert.True(t, recorder.IsStoryNotFound(err), "unresolvable story must be typed, got: %v", err)
}
