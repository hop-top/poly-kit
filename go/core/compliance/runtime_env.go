package compliance

// Runtime-check harness for F13 (ConsentingTelemetry) sub-checks.
//
// rtEnv spawns adopter binaries in an isolated HOME/XDG tree and
// captures emitted telemetry events from a JSONL sink file. Each
// scenario gets a fresh tmpdir so first-run state (consent prompt,
// install_id materialization) does not bleed between checks.
//
// The harness wires the following envs into every subprocess via
// e.Env: HOME, XDG_CONFIG_HOME, XDG_STATE_HOME, KIT_BUS_SINK=jsonl,
// KIT_BUS_SINK_PATH=<e.BusFile>. KIT_BUS_SINK is the contract from
// ADR-0037 sub-condition (c)/(d) — the bus package does not yet
// honor it directly (no env-driven sink switch in
// hops/main/go/runtime/bus/ as of T-0700), so adopter binaries that
// want runtime-check observability must either plumb the env into
// their bus builder themselves OR route emission to a JSONLSink at
// KIT_BUS_SINK_PATH. Runtime checks (T-0701..T-0703) may also use
// KIT_TELEMETRY_ENDPOINT pointed at an httptest server when the
// adopter ships only the HTTPS sink; the harness's BusFile path
// reads whatever was written there regardless of the routing path.
//
// Public surface is file-scoped (unexported) per T-0700 — the
// harness is consumed only by sibling _test.go files in this
// package.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rtEnv is an isolated test environment for one runtime check
// scenario. Each check needs a fresh slate — first-run state
// (consent prompt, install_id) bleeds between scenarios when reused.
//
// Two construction paths exist:
//
//   - newRTEnv(t *testing.T) — test convenience; uses t.TempDir + t.Logf.
//   - newRTEnvDir(dir string) — production-friendly; caller manages
//     the tmpdir lifecycle (os.MkdirTemp + defer os.RemoveAll).
//
// The split lets production code under Run/RunRuntime (T-0704) invoke
// the runtime sub-checks without dragging *testing.T through. Tests
// keep the t.TempDir convenience.
type rtEnv struct {
	HomeDir   string   // tmpdir, also HOME env
	XDGConfig string   // <HomeDir>/.config
	XDGState  string   // <HomeDir>/.local/state
	BusFile   string   // <HomeDir>/bus.jsonl — jsonl sink target
	Env       []string // os.Environ-compatible slice for exec.Command.Env
	// logf is the diagnostic logger; nil-safe via the logf method.
	// Set to t.Logf by newRTEnv; defaulted to no-op by newRTEnvDir
	// (production callers route diagnostics through their own loggers
	// via the surrounding orchestration, not the harness).
	logf func(format string, args ...any)
}

// newRTEnv constructs a fresh isolated env. Auto-registers t.Cleanup
// to remove the tmpdir at test end (t.TempDir handles the cleanup
// itself; the explicit Cleanup belt-and-braces against future
// changes that switch off t.TempDir). Sets HOME, XDG_CONFIG_HOME,
// XDG_STATE_HOME, KIT_BUS_SINK=jsonl, KIT_BUS_SINK_PATH=<BusFile>
// in the Env slice, stripping any inherited values for those keys
// first so a test runner with HOME=/sentinel cannot leak into the
// subprocess.
func newRTEnv(t *testing.T) *rtEnv {
	t.Helper()
	e := newRTEnvDir(t.TempDir())
	e.logf = t.Logf
	return e
}

// newRTEnvDir constructs a fresh isolated env rooted at dir. The
// caller owns dir's lifecycle — typical production usage is
// `dir, _ := os.MkdirTemp("", "kit-compliance-*"); defer os.RemoveAll(dir)`.
// Diagnostics route through a no-op logger by default; assign e.logf
// to plumb them into a real logger.
func newRTEnvDir(dir string) *rtEnv {
	config := filepath.Join(dir, ".config")
	state := filepath.Join(dir, ".local", "state")
	busFile := filepath.Join(dir, "bus.jsonl")

	// Best-effort mkdir; failure here surfaces in subprocess invocations.
	// We don't have a *testing.T to Fatalf with; production callers
	// inspect the eventual CheckResult Details for failures, and tests
	// route through newRTEnv (which uses t.TempDir + already-existing
	// dir, so MkdirAll is guaranteed to succeed in practice).
	_ = os.MkdirAll(config, 0o700)
	_ = os.MkdirAll(state, 0o700)
	// Do NOT pre-create busFile; the sink creates it on first write.

	env := stripEnv(os.Environ(),
		"HOME",
		"XDG_CONFIG_HOME",
		"XDG_STATE_HOME",
		"KIT_BUS_SINK",
		"KIT_BUS_SINK_PATH",
	)
	env = append(env,
		"HOME="+dir,
		"XDG_CONFIG_HOME="+config,
		"XDG_STATE_HOME="+state,
		"KIT_BUS_SINK=jsonl",
		"KIT_BUS_SINK_PATH="+busFile,
	)

	return &rtEnv{
		HomeDir:   dir,
		XDGConfig: config,
		XDGState:  state,
		BusFile:   busFile,
		Env:       env,
	}
}

// log is a nil-safe diagnostic logger. newRTEnv wires t.Logf;
// newRTEnvDir leaves logf nil, in which case messages are dropped.
func (e *rtEnv) log(format string, args ...any) {
	if e.logf != nil {
		e.logf(format, args...)
	}
}

// stripEnv returns env with any entry whose key (LHS of `=`) matches
// one of keys removed. Used by newRTEnv to scrub inherited overrides
// before appending the harness's canonical values.
func stripEnv(env []string, keys ...string) []string {
	if len(env) == 0 || len(keys) == 0 {
		return env
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		skip := false
		for _, k := range keys {
			if strings.HasPrefix(kv, k+"=") {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, kv)
		}
	}
	return out
}

// Run invokes bin with args under the rtEnv's environment. Returns
// stdout, stderr, exit code. ctx with a deadline is highly
// recommended — sub-checks that hang otherwise (e.g. a binary
// awaiting a TTY prompt) would block the whole test suite. cwd is
// set to e.HomeDir so a misbehaving binary that writes relative
// paths lands inside the tmpdir.
func (e *rtEnv) Run(ctx context.Context, bin string, args ...string) (stdout, stderr string, exitCode int) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = e.Env
	cmd.Dir = e.HomeDir
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode = 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		e.log("rtEnv.Run %s: %v", bin, err)
		exitCode = -1
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// EventsEmitted tails e.BusFile and returns parsed JSONL events,
// one map per line. Malformed lines are skipped with a t.Logf
// warning so a single bad payload does not abort the read. Returns
// nil (not an error) when the file does not exist yet — sinks
// create it lazily, so "no events" and "no file" are the same
// outcome for the caller.
func (e *rtEnv) EventsEmitted() []map[string]any {
	f, err := os.Open(e.BusFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		e.log("rtEnv.EventsEmitted: open %s: %v", e.BusFile, err)
		return nil
	}
	defer f.Close()

	var events []map[string]any
	scanner := bufio.NewScanner(f)
	// Telemetry payloads (especially redacted-rule audit events) can
	// exceed the default 64KiB bufio scanner buffer. 1 MiB matches
	// the bus package's own redact-audit cap (ADR-0035 §4).
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			e.log("rtEnv.EventsEmitted: malformed line: %v", err)
			continue
		}
		events = append(events, m)
	}
	if err := scanner.Err(); err != nil {
		e.log("rtEnv.EventsEmitted: scan %s: %v", e.BusFile, err)
	}
	return events
}

// PollEvents waits up to maxWait for EventsEmitted() to return at
// least minCount events. Returns the events seen (may exceed
// minCount) with ok=true on satisfaction, or (events-so-far, false)
// if the deadline expires first. Used by sub-checks that emit
// asynchronously and need a bounded wait without sleeping the full
// budget.
func (e *rtEnv) PollEvents(minCount int, maxWait time.Duration) ([]map[string]any, bool) {
	const interval = 25 * time.Millisecond
	deadline := time.Now().Add(maxWait)
	for {
		evs := e.EventsEmitted()
		if len(evs) >= minCount {
			return evs, true
		}
		if time.Now().After(deadline) {
			return evs, false
		}
		time.Sleep(interval)
	}
}

// SeedConsent writes a telemetry.yaml file under
// <XDG_CONFIG_HOME>/kit/ with the given decision. Helper for runtime
// checks that need a granted-state fixture without driving the
// interactive prompt. Shape mirrors consent.FileStore's wire format
// (ADR-0036): telemetry.consent.{state, decided_at, prompt_version,
// decision_source}. decided_at is hardcoded to a fixed timestamp
// because runtime checks should not depend on wall-clock for
// determinism.
func (e *rtEnv) SeedConsent(state, source string, promptVersion int) error {
	consentDir := filepath.Join(e.XDGConfig, "kit")
	if err := os.MkdirAll(consentDir, 0o700); err != nil {
		return fmt.Errorf("SeedConsent: mkdir %s: %w", consentDir, err)
	}
	consentFile := filepath.Join(consentDir, "telemetry.yaml")
	content := fmt.Sprintf(`telemetry:
  consent:
    state: %s
    decided_at: 2026-05-19T12:00:00Z
    prompt_version: %d
    decision_source: %s
`, state, promptVersion, source)
	if err := os.WriteFile(consentFile, []byte(content), 0o600); err != nil {
		return fmt.Errorf("SeedConsent: write %s: %w", consentFile, err)
	}
	return nil
}

// SetEnv overrides a single env var in the rtEnv's Env slice.
// Replaces an existing entry by key (LHS of `=`); appends when the
// key is absent. Lets sub-checks toggle e.g. DO_NOT_TRACK or
// KIT_TELEMETRY_MODE without rebuilding the slice from scratch.
func (e *rtEnv) SetEnv(key, value string) {
	prefix := key + "="
	for i, kv := range e.Env {
		if strings.HasPrefix(kv, prefix) {
			e.Env[i] = prefix + value
			return
		}
	}
	e.Env = append(e.Env, prefix+value)
}

// UnsetEnv removes a key from the Env slice. Used to negate a
// harness default for a single scenario (e.g. unset KIT_BUS_SINK to
// verify the binary errors instead of silently emitting to stdout).
func (e *rtEnv) UnsetEnv(key string) {
	prefix := key + "="
	out := e.Env[:0]
	for _, kv := range e.Env {
		if !strings.HasPrefix(kv, prefix) {
			out = append(out, kv)
		}
	}
	e.Env = out
}
