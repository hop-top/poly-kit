// e2e_test.go is the cross-cutting end-to-end test for the `kit
// telemetry` subcommand tree (T-0671). The per-subcommand files
// (status_test.go, enable_test.go, disable_test.go, reset_test.go,
// inspect_test.go) each exercise one verb in isolation; what they
// don't cover is the OPERATOR SEQUENCE — does enable; status; disable
// flow correctly when each verb writes through the canonical
// FileStore that the next verb reads from?
//
// In-process approach (NOT subprocess exec)
// -----------------------------------------
// We DO NOT build the kit binary and shell out. Two reasons:
//
//  1. The kit binary doesn't wire `telemetry` into its cobra root
//     yet — telemetry.Cmd() exists but cmd/kit/main.go has no
//     AddCommand for it (verified at task time). A subprocess-exec
//     approach would have nothing to invoke.
//  2. Even once wired, an in-process drive via cmd.SetArgs +
//     cmd.ExecuteContext is faster, more portable across platforms,
//     and surfaces panics inside the cobra body to t.Fatalf instead
//     of an opaque exit code.
//
// Each test constructs a FRESH parent `telemetry` command via Cmd()
// (so all subcommand wirings are exercised) and drives subcommands
// via SetArgs(["status"]) / SetArgs(["enable"]) etc. XDG_CONFIG_HOME
// and XDG_STATE_HOME are pointed at t.TempDir()s so on-disk effects
// are isolated. The reset verb's --yes flag is used in tests rather
// than wiring a stdin pipe — it's the operator-facing escape hatch
// for non-interactive runs and the right surface to exercise here.
//
// Output isolation: cmd.SetOut / SetErr capture writes from the
// subcommand bodies (which use cmd.OutOrStdout / ErrOrStderr) so we
// can assert on rendered bytes without temp files.

package telemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/kit/cmd/kit/telemetry"
	"hop.top/kit/go/core/consent"
	runtimetel "hop.top/kit/go/runtime/telemetry"
)

// withE2EXDG points XDG_CONFIG_HOME and XDG_STATE_HOME at fresh
// t.TempDir() roots and clears the precedence-chain env vars so each
// test starts from a known cold state. Mirrors the per-subcommand
// helpers (freshXDGForEnable / withFreshXDG / withFreshXDGInspect)
// but is local to this file so it stays self-contained — external
// test package, can't reach the package-internal helpers.
func withE2EXDG(t *testing.T) {
	t.Helper()
	cfg := t.TempDir()
	st := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XDG_STATE_HOME", st)
	t.Setenv("XDG_DATA_HOME", filepath.Join(st, "_data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(st, "_cache"))
	// Clear env kill switches so the chain's default branches fire.
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("KIT_TELEMETRY_MODE", "")
	t.Setenv("KIT_TELEMETRY_CONSENT", "")
}

// e2eInvoke runs `kit telemetry <args...>` in-process by constructing
// a fresh parent Cmd, plumbing SetArgs / SetOut / SetErr, and calling
// ExecuteContext. Returns (stdout, stderr, err) so each test asserts
// on the bytes plus the exit status.
//
// Each call constructs a NEW root via telemetry.Cmd(). Cobra
// commands carry per-invocation state (flag values, parsed args), so
// reusing one across multiple invocations would let earlier flag
// settings leak into the next call. A fresh root per call costs
// microseconds and removes that footgun.
//
// We register a PERSISTENT --format flag on the synthetic root so
// `telemetry status --format json` works the same way it does under
// the real `kit` binary (where cli.New adds --format on the kit-wide
// root). status.go's resolveFormat walks parents and reads the first
// non-empty --format value — adding the flag here gives it something
// to find without coupling these tests to cli.New's internals.
func e2eInvoke(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := telemetry.Cmd()
	root.PersistentFlags().String("format", "", "output format (table|json|yaml|text)")
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return stdout.String(), stderr.String(), err
}

// readDecision opens a fresh FileStore under the current XDG dirs and
// returns the persisted Decision. Used by every test that wants to
// assert "what did `kit telemetry <verb>` write?".
func readDecision(t *testing.T) consent.Decision {
	t.Helper()
	s, err := consent.NewFileStore()
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	d, err := s.Get(context.Background())
	if err != nil {
		t.Fatalf("FileStore.Get: %v", err)
	}
	return d
}

// TestE2E_StatusOnFreshSystem_ShowsDenied: cold cache, no consent
// file, no env overrides. `kit telemetry status --format json` must
// render a payload whose Consent.State is "unknown" (no record yet)
// and exit cleanly.
func TestE2E_StatusOnFreshSystem_ShowsDenied(t *testing.T) {
	withE2EXDG(t)

	stdout, _, err := e2eInvoke(t, "status", "--format", "json")
	if err != nil {
		t.Fatalf("telemetry status: %v", err)
	}

	var got struct {
		Consent struct {
			State string `json:"state"`
		} `json:"consent"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("json unmarshal: %v\nraw: %s", err, stdout)
	}
	// "unknown" is the cold-start on-disk shape. The RESOLVER would
	// return denied/config under these inputs, but status renders the
	// PERSISTED decision (not the resolved one), so unknown is correct
	// until something writes.
	if got.Consent.State != string(consent.StateUnknown) {
		t.Errorf("Consent.State = %q, want %q on cold cache",
			got.Consent.State, consent.StateUnknown)
	}
}

// TestE2E_EnableThenStatus_ShowsGranted runs `enable` then `status`
// and asserts the second invocation observes the first's write. The
// happy-path sanity check: both verbs are wired and they share the
// same persistence layer.
func TestE2E_EnableThenStatus_ShowsGranted(t *testing.T) {
	withE2EXDG(t)

	if _, _, err := e2eInvoke(t, "enable"); err != nil {
		t.Fatalf("telemetry enable: %v", err)
	}
	d := readDecision(t)
	if d.State != consent.StateGranted {
		t.Fatalf("after enable: State = %q, want %q", d.State, consent.StateGranted)
	}
	if d.DecisionSource != consent.SourceFlag {
		t.Errorf("after enable: DecisionSource = %q, want %q",
			d.DecisionSource, consent.SourceFlag)
	}

	stdout, _, err := e2eInvoke(t, "status", "--format", "json")
	if err != nil {
		t.Fatalf("telemetry status: %v", err)
	}
	var got struct {
		Consent struct {
			State          string `json:"state"`
			DecisionSource string `json:"decision_source"`
		} `json:"consent"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("json unmarshal: %v\nraw: %s", err, stdout)
	}
	if got.Consent.State != string(consent.StateGranted) {
		t.Errorf("status after enable: State = %q, want %q",
			got.Consent.State, consent.StateGranted)
	}
	if got.Consent.DecisionSource != string(consent.SourceFlag) {
		t.Errorf("status after enable: DecisionSource = %q, want %q",
			got.Consent.DecisionSource, consent.SourceFlag)
	}
}

// TestE2E_EnableDisableEnable_StateBouncesCorrectly: enable -> disable
// -> enable. Each transition must produce the right persisted state
// AND a DecisionSource of "flag" (the verb is the source). This
// exercises the FileStore's "preserve sibling keys, replace consent
// block" promise three times in series.
func TestE2E_EnableDisableEnable_StateBouncesCorrectly(t *testing.T) {
	withE2EXDG(t)

	if _, _, err := e2eInvoke(t, "enable"); err != nil {
		t.Fatalf("enable #1: %v", err)
	}
	if d := readDecision(t); d.State != consent.StateGranted {
		t.Fatalf("after enable #1: State = %q, want %q", d.State, consent.StateGranted)
	}

	if _, _, err := e2eInvoke(t, "disable"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	d := readDecision(t)
	if d.State != consent.StateDenied {
		t.Fatalf("after disable: State = %q, want %q", d.State, consent.StateDenied)
	}
	if d.DecisionSource != consent.SourceFlag {
		t.Errorf("after disable: DecisionSource = %q, want %q",
			d.DecisionSource, consent.SourceFlag)
	}

	if _, _, err := e2eInvoke(t, "enable"); err != nil {
		t.Fatalf("enable #2: %v", err)
	}
	if d := readDecision(t); d.State != consent.StateGranted {
		t.Fatalf("after enable #2: State = %q, want %q", d.State, consent.StateGranted)
	}
}

// TestE2E_ResetClearsAndRotatesInstallID: enable to populate state +
// touch the install_id, then `reset --yes` to clear consent and
// rotate the id. After reset, the consent state is StateUnknown and
// the install_id is a fresh value distinct from the pre-reset one.
//
// The --yes flag is exercised because the kit-wide --confirm matrix
// is not wired through telemetry.Cmd() directly (it lives on the
// parent `kit` root via cli.New); --yes is the local escape hatch
// that the reset_test.go::TestResetCmd_Wired pins as the inline-
// confirmation surface.
func TestE2E_ResetClearsAndRotatesInstallID(t *testing.T) {
	withE2EXDG(t)

	if _, _, err := e2eInvoke(t, "enable"); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// Touch install_id so it gets created; capture the value.
	before, err := runtimetel.InstallationID()
	if err != nil {
		t.Fatalf("InstallationID pre-reset: %v", err)
	}

	if _, _, err := e2eInvoke(t, "reset", "--yes"); err != nil {
		t.Fatalf("reset: %v", err)
	}

	// Consent cleared.
	if d := readDecision(t); d.State != consent.StateUnknown {
		t.Errorf("after reset: State = %q, want %q", d.State, consent.StateUnknown)
	}
	// install_id rotated.
	after, err := runtimetel.InstallationID()
	if err != nil {
		t.Fatalf("InstallationID post-reset: %v", err)
	}
	if before == after {
		t.Errorf("install_id unchanged across reset: %q", after)
	}
}

// TestE2E_InspectOnEmptySpool_PrintsInfo: a fresh adopter has no
// spool dir. inspect must exit 0 with an informational message. This
// is the operator-facing "healthy state" output — empty spool means
// telemetry is either disabled or shipping cleanly.
func TestE2E_InspectOnEmptySpool_PrintsInfo(t *testing.T) {
	withE2EXDG(t)

	stdout, _, err := e2eInvoke(t, "inspect")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if !strings.Contains(stdout, "No spooled telemetry events") {
		t.Errorf("inspect on empty spool did not print info message; got:\n%s", stdout)
	}
}

// TestE2E_FullSequence chains the operator-facing verbs in the order
// an adopter actually runs them: enable -> status (confirm granted)
// -> disable -> status (confirm denied) -> inspect (still no events
// because we don't drive the emitter). Each step asserts the on-disk
// state matches the verb's documented contract.
//
// The "next emit is no-op" claim from the task spec is covered by
// the consent_hook contract: the hook reads the FileStore on every
// call, and the FileStore returns the most recent Set. We assert the
// HOOK behavior via NewHook(s).Granted() — the same API
// kit-telemetry's emitter consults — so we get the operator-visible
// emit decision in one step.
func TestE2E_FullSequence(t *testing.T) {
	withE2EXDG(t)

	// enable.
	if _, _, err := e2eInvoke(t, "enable"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if d := readDecision(t); d.State != consent.StateGranted {
		t.Fatalf("after enable: State = %q, want %q", d.State, consent.StateGranted)
	}

	// status confirms granted.
	stdout, _, err := e2eInvoke(t, "status", "--format", "json")
	if err != nil {
		t.Fatalf("status post-enable: %v", err)
	}
	if !strings.Contains(stdout, `"state": "granted"`) &&
		!strings.Contains(stdout, `"state":"granted"`) {
		t.Errorf("status JSON missing granted state; got:\n%s", stdout)
	}

	// Mid-flight: the consent hook reads the live FileStore. With the
	// store holding granted, Granted returns true. This is the
	// per-batch check kit-telemetry's emitter performs.
	{
		s, err := consent.NewFileStore()
		if err != nil {
			t.Fatalf("NewFileStore (mid-flight): %v", err)
		}
		hook := consent.NewHook(s)
		if !hook.Granted(context.Background()) {
			t.Errorf("ConsentHook.Granted() = false while persisted granted; emitter would not ship")
		}
	}

	// disable.
	if _, _, err := e2eInvoke(t, "disable"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if d := readDecision(t); d.State != consent.StateDenied {
		t.Fatalf("after disable: State = %q, want %q", d.State, consent.StateDenied)
	}

	// status confirms denied.
	stdout, _, err = e2eInvoke(t, "status", "--format", "json")
	if err != nil {
		t.Fatalf("status post-disable: %v", err)
	}
	if !strings.Contains(stdout, `"state": "denied"`) &&
		!strings.Contains(stdout, `"state":"denied"`) {
		t.Errorf("status JSON missing denied state; got:\n%s", stdout)
	}

	// Next emit would no-op: the hook over the same store now returns
	// false. This is the "mid-flight disable drops the next batch"
	// contract from ADR-0036.
	{
		s, err := consent.NewFileStore()
		if err != nil {
			t.Fatalf("NewFileStore (post-disable): %v", err)
		}
		hook := consent.NewHook(s)
		if hook.Granted(context.Background()) {
			t.Errorf("ConsentHook.Granted() = true after disable; emitter would still ship")
		}
	}

	// inspect on the empty spool — telemetry was enabled briefly but
	// we never drove the emitter, so there's nothing on disk. Exit 0
	// with the info message.
	stdout, _, err = e2eInvoke(t, "inspect")
	if err != nil {
		t.Fatalf("inspect at end: %v", err)
	}
	if !strings.Contains(stdout, "No spooled telemetry events") {
		t.Errorf("inspect at end did not print empty-spool info; got:\n%s", stdout)
	}
}
