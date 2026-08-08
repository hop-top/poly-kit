// Package recorder turns a conformance scenario plus a target binary
// into an uploadable cassette. It walks the scenario's steps[], runs
// each invoke against the binary as a real subprocess, captures exit
// code, stdout, stderr, and duration verbatim, and writes the svc
// upload layout:
//
//	<out>/manifest.yaml                     svc-schema manifest
//	<out>/story.yaml                        byte-exact story copy
//	<out>/steps/<id>/result.json            {"exit_code": N, "duration_ms": M}
//	<out>/steps/<id>/stdout.txt             stdout as observed
//	<out>/steps/<id>/stderr.txt             stderr as observed
//	<out>/steps/<id>/cassette/              xrr cassette (when the step records one)
//
// Captures are recorded, never authored: every byte comes from the
// subprocess. Each step exports XRR_CASSETTE_DIR + XRR_MODE=record so
// binaries instrumented with xrr sessions persist their adapter
// traffic into the per-step cassette dir.
//
// The emitted manifest passes svc.ValidateManifest and the packed
// tree is accepted by the svc cassette receiver, so a recorded dir
// grades cleanly through `conformance grade`.
package recorder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"hop.top/kit/go/conformance/scenario"
	"hop.top/kit/go/conformance/svc"
	"hop.top/kit/go/console/cli/conformance/harness"
)

// Options carries everything Run needs. Scenario, StoryBytes, OutDir,
// and Invoker are required; the rest default sensibly.
type Options struct {
	// Scenario is the parsed + validated scenario document whose
	// steps[] drive the recording.
	Scenario *scenario.Scenario
	// StoryBytes is the story source the cassette ships. Run refuses
	// to record when sha256(StoryBytes) does not match the scenario's
	// story_ref.content_hash — hash drift at record time means the
	// scenario and story went out of sync.
	StoryBytes []byte
	// OutDir is the cassette destination. It must not exist or must
	// be an empty directory; Run never overwrites prior captures.
	OutDir string
	// Invoker executes one step. ExecInvoker runs the target binary
	// as a subprocess; tests may substitute their own.
	Invoker harness.Invoker

	// Binary names the recorded binary in the manifest. Default:
	// Scenario.Binary.
	Binary string
	// BinaryVersion is an optional version stamp (git SHA, semver).
	BinaryVersion string
	// ScenarioID is the manifest scenario_id, typically the
	// namespace-qualified "<ns>/<id>" ref the grading service routes
	// on. Default: Scenario.ScenarioID (unqualified).
	ScenarioID string
	// ScenarioVersion is the manifest scenario_version.
	ScenarioVersion string
	// RecorderVersion overrides the recorder_version stamp. Default:
	// the xrr module version compiled into the binary.
	RecorderVersion string
	// Now supplies the recorded_at clock. Default: time.Now.
	Now func() time.Time
}

// StepOutcome is one step's observed result.
type StepOutcome struct {
	ID         string `json:"id"          yaml:"id"          table:"STEP"`
	ExitCode   int    `json:"exit_code"   yaml:"exit_code"   table:"EXIT"`
	DurationMS int64  `json:"duration_ms" yaml:"duration_ms" table:"MS"`
}

// Result is the recording summary Run returns.
type Result struct {
	OutDir       string        `json:"out_dir"       yaml:"out_dir"`
	ManifestPath string        `json:"manifest_path" yaml:"manifest_path"`
	ScenarioID   string        `json:"scenario_id"   yaml:"scenario_id"`
	StoryHash    string        `json:"story_hash"    yaml:"story_hash"`
	Steps        []StepOutcome `json:"steps"         yaml:"steps"`
	Manifest     *svc.Manifest `json:"-"             yaml:"-"`
}

// Run records opts.Scenario against the configured invoker and
// writes the cassette to opts.OutDir. The returned Result summarizes
// the run; the on-disk manifest has already passed
// svc.ValidateManifest when Run returns nil.
func Run(ctx context.Context, opts Options) (*Result, error) {
	if opts.Scenario == nil {
		return nil, fmt.Errorf("recorder: Scenario is required")
	}
	if len(opts.StoryBytes) == 0 {
		return nil, fmt.Errorf("recorder: StoryBytes is required")
	}
	if opts.OutDir == "" {
		return nil, fmt.Errorf("recorder: OutDir is required")
	}
	if opts.Invoker == nil {
		return nil, fmt.Errorf("recorder: Invoker is required")
	}

	// Content-hash guard: the story we ship must be the story the
	// scenario was authored against.
	sum := sha256.Sum256(opts.StoryBytes)
	gotHash := "sha256:" + hex.EncodeToString(sum[:])
	if declared := opts.Scenario.StoryRef.ContentHash; !strings.EqualFold(declared, gotHash) {
		return nil, &StoryHashMismatchError{Declared: declared, Actual: gotHash}
	}

	if err := ensureEmptyDir(opts.OutDir); err != nil {
		return nil, err
	}

	// story.yaml: byte-exact copy.
	if err := os.WriteFile(filepath.Join(opts.OutDir, "story.yaml"), opts.StoryBytes, 0o644); err != nil {
		return nil, fmt.Errorf("recorder: write story.yaml: %w", err)
	}

	res := &Result{
		OutDir:     opts.OutDir,
		ScenarioID: manifestScenarioID(opts),
		StoryHash:  gotHash,
	}

	manifestSteps := make([]svc.ManifestStep, 0, len(opts.Scenario.Steps))
	for i := range opts.Scenario.Steps {
		st := &opts.Scenario.Steps[i]
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("recorder: canceled before step %q: %w", st.ID, err)
		}
		outcome, err := recordStep(ctx, opts, st)
		if err != nil {
			return nil, err
		}
		res.Steps = append(res.Steps, *outcome)
		manifestSteps = append(manifestSteps, svc.ManifestStep{
			ID:          st.ID,
			CassetteDir: "steps/" + st.ID + "/cassette",
			Captures:    "steps/" + st.ID,
		})
	}

	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	m := &svc.Manifest{
		SchemaVersion:   "1",
		Binary:          manifestBinary(opts),
		BinaryVersion:   opts.BinaryVersion,
		Recorder:        "xrr",
		RecorderVersion: recorderVersion(opts.RecorderVersion),
		RecordedAt:      now().UTC().Truncate(time.Second),
		ScenarioID:      res.ScenarioID,
		ScenarioVersion: opts.ScenarioVersion,
		StoryRef: svc.ManifestStory{
			StoryID:     opts.Scenario.StoryRef.StoryID,
			StoryPath:   "story.yaml",
			ContentHash: gotHash,
		},
		Steps: manifestSteps,
	}
	if err := svc.ValidateManifest(m); err != nil {
		return nil, fmt.Errorf("recorder: emitted manifest failed validation: %w", err)
	}
	encoded, err := encodeManifest(m)
	if err != nil {
		return nil, fmt.Errorf("recorder: encode manifest: %w", err)
	}
	res.ManifestPath = filepath.Join(opts.OutDir, "manifest.yaml")
	if err := os.WriteFile(res.ManifestPath, encoded, 0o644); err != nil {
		return nil, fmt.Errorf("recorder: write manifest.yaml: %w", err)
	}
	res.Manifest = m
	return res, nil
}

// recordStep runs one scenario step and persists its capture set.
func recordStep(ctx context.Context, opts Options, st *scenario.Step) (*StepOutcome, error) {
	if len(st.Invoke) == 0 {
		return nil, fmt.Errorf("recorder: step %q has an empty invoke", st.ID)
	}
	if st.Delay != "" {
		d, err := time.ParseDuration(st.Delay)
		if err != nil {
			return nil, fmt.Errorf("recorder: step %q delay %q: %w", st.ID, st.Delay, err)
		}
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return nil, fmt.Errorf("recorder: canceled during step %q delay: %w", st.ID, ctx.Err())
		}
	}

	stepDir := filepath.Join(opts.OutDir, "steps", st.ID)
	cassetteDir := filepath.Join(stepDir, "cassette")
	if err := os.MkdirAll(cassetteDir, 0o755); err != nil {
		return nil, fmt.Errorf("recorder: mkdir step %q: %w", st.ID, err)
	}

	// Per-step env: scenario-declared vars plus the xrr recording
	// substrate so instrumented binaries persist adapter traffic.
	env := make(map[string]string, len(st.Env)+2)
	for k, v := range st.Env {
		env[k] = v
	}
	absCassette, err := filepath.Abs(cassetteDir)
	if err != nil {
		return nil, fmt.Errorf("recorder: abs cassette dir: %w", err)
	}
	env["XRR_CASSETTE_DIR"] = absCassette
	env["XRR_MODE"] = "record"

	stdout, err := os.Create(filepath.Join(stepDir, "stdout.txt"))
	if err != nil {
		return nil, fmt.Errorf("recorder: create stdout.txt: %w", err)
	}
	defer stdout.Close()
	stderr, err := os.Create(filepath.Join(stepDir, "stderr.txt"))
	if err != nil {
		return nil, fmt.Errorf("recorder: create stderr.txt: %w", err)
	}
	defer stderr.Close()

	// invoke[0] is the binary token from the scenario; the invoker is
	// already bound to the target binary path, so only args pass on.
	args := st.Invoke[1:]
	start := time.Now()
	exit, runErr := opts.Invoker.Invoke(args, strings.NewReader(st.Stdin), stdout, stderr, env)
	durationMS := time.Since(start).Milliseconds()
	if runErr != nil {
		return nil, &StepExecError{StepID: st.ID, Err: runErr}
	}
	if err := stdout.Sync(); err != nil {
		return nil, fmt.Errorf("recorder: flush stdout.txt: %w", err)
	}
	if err := stderr.Sync(); err != nil {
		return nil, fmt.Errorf("recorder: flush stderr.txt: %w", err)
	}

	resultJSON := fmt.Sprintf("{\"exit_code\": %d, \"duration_ms\": %d}\n", exit, durationMS)
	if err := os.WriteFile(filepath.Join(stepDir, "result.json"), []byte(resultJSON), 0o644); err != nil {
		return nil, fmt.Errorf("recorder: write result.json: %w", err)
	}

	// Drop the cassette dir when the step recorded nothing — the
	// manifest still names it; the layout stays clean like a
	// hand-assembled cassette.
	if entries, err := os.ReadDir(cassetteDir); err == nil && len(entries) == 0 {
		_ = os.Remove(cassetteDir)
	}

	return &StepOutcome{ID: st.ID, ExitCode: exit, DurationMS: durationMS}, nil
}

// ensureEmptyDir creates dir if missing and refuses a non-empty one.
func ensureEmptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	switch {
	case os.IsNotExist(err):
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("recorder: mkdir out dir: %w", err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("recorder: read out dir: %w", err)
	case len(entries) > 0:
		return &OutDirNotEmptyError{Dir: dir}
	}
	return nil
}

func manifestBinary(opts Options) string {
	if opts.Binary != "" {
		return opts.Binary
	}
	return opts.Scenario.Binary
}

func manifestScenarioID(opts Options) string {
	if opts.ScenarioID != "" {
		return opts.ScenarioID
	}
	return opts.Scenario.ScenarioID
}

// recorderVersion resolves the recorder_version stamp: explicit
// override first, then the xrr module version from build info, then
// a stable fallback (the manifest schema requires a non-empty value).
func recorderVersion(override string) string {
	if override != "" {
		return override
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == "hop.top/xrr" {
				return strings.TrimPrefix(dep.Version, "v")
			}
		}
	}
	return "0.0.0-unknown"
}

// encodeManifest serializes the manifest to deterministic YAML: keys
// in struct-declaration order, 2-space indent, RFC3339 timestamps,
// omitempty fields elided.
func encodeManifest(m *svc.Manifest) ([]byte, error) {
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return []byte(buf.String()), nil
}
