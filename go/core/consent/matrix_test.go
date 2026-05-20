// matrix_test.go is the cross-cutting integration matrix for the
// kit-consent slice. Sibling files own unit tests over a single
// layer:
//
//   - consent_test.go: schema + FileStore round-trips
//   - resolve_test.go: precedence chain in isolation (pure resolver)
//   - hook_test.go:    Store -> ConsentHook adapter
//   - prompt_test.go (CLI): first-run prompt branches
//   - status / enable / disable / reset / inspect _test.go (CLI): per-
//     subcommand bodies
//
// What's missing from those is the COMBINATION: does the resolver
// reading from a real FileStore behave the same way the unit tests
// implied? Does a stale prompt_version persisted by one component
// trigger the correct branch in another? Does the load-bearing
// "DO_NOT_TRACK beats every other layer" claim hold when every other
// layer is at its most aggressive?
//
// This file deliberately uses the EXTERNAL test package (consent_test)
// so it only reaches the public API surface — Store, Decision,
// Resolve, NewFileStore. If a scenario needs an internal seam, it lives
// in one of the per-layer unit files, not here.
//
// NOTE: each test creates its own t.TempDir XDG_CONFIG_HOME via
// t.Setenv. The FileStore reads that env at NewFileStore() time, so
// tests MUST construct the store AFTER calling withMatrixXDG.

package consent_test

import (
	"context"
	"testing"
	"time"

	"hop.top/kit/go/core/consent"
)

// withMatrixXDG points XDG_CONFIG_HOME at a fresh t.TempDir() for the
// duration of the test. Mirrors the consent package's internal
// newTestStore helper but stays on the public surface — we don't need
// the *FileStore handle here, we want callers to construct theirs via
// NewFileStore() so each test exercises the real path resolution.
func withMatrixXDG(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	// Defensive: clear DO_NOT_TRACK + KIT_TELEMETRY_MODE so the test's
	// own t.Setenv calls drive the precedence chain, not whatever the
	// host shell has set. t.Setenv auto-restores at cleanup.
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("KIT_TELEMETRY_MODE", "")
	t.Setenv("KIT_TELEMETRY_CONSENT", "")
}

// matrixFixedNow is the canonical clock used when seeding a Decision.
// Cross-cutting tests do not assert on the actual value — they assert
// on which branch fired — so any deterministic instant works.
func matrixFixedNow() time.Time {
	return time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
}

// seedMatrixDecision writes d through a fresh FileStore so subsequent
// resolver/hook reads observe the canonical on-disk shape. Tests that
// need to inspect the path use NewFileStore() themselves; this helper
// exists to keep the seed call sites short.
func seedMatrixDecision(t *testing.T, d consent.Decision) {
	t.Helper()
	s, err := consent.NewFileStore()
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := s.Set(context.Background(), d); err != nil {
		t.Fatalf("Set: %v", err)
	}
}

// loadMatrixDecision returns whatever the FileStore currently holds on
// disk via the public Get API. Used by tests that need to assert
// "what's persisted?" without rebuilding a Store every time.
func loadMatrixDecision(t *testing.T) consent.Decision {
	t.Helper()
	s, err := consent.NewFileStore()
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	d, err := s.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return d
}

// TestMatrix_NonTTY_FirstRun_PersistsDeniedConfig exercises the full
// chain a CI invocation would see on a cold cache: no persisted
// decision, no env overrides, the resolver short-circuits at step 7
// to denied/config WITHOUT touching disk. The prompt-driven persist
// happens one layer up (cmd/kit/telemetry's promptInternal) and is
// covered by its own tests; the resolver-only assertion here is that
// the cold-start default is denied/config and the FileStore reports
// StateUnknown until something explicitly writes.
func TestMatrix_NonTTY_FirstRun_PersistsDeniedConfig(t *testing.T) {
	withMatrixXDG(t)

	// Resolver path: empty inputs, empty env -> denied/config.
	d := consent.Resolve(context.Background(), consent.Inputs{
		Env: consent.MapEnv(nil),
	})
	if d.State != consent.StateDenied {
		t.Errorf("State = %q, want %q", d.State, consent.StateDenied)
	}
	if d.DecisionSource != consent.SourceConfig {
		t.Errorf("DecisionSource = %q, want %q", d.DecisionSource, consent.SourceConfig)
	}

	// Store path: no write happened, so Get returns StateUnknown.
	if got := loadMatrixDecision(t); got.State != consent.StateUnknown {
		t.Errorf("on-disk State = %q, want %q (resolver default-deny must NOT touch disk)",
			got.State, consent.StateUnknown)
	}
}

// TestMatrix_DoNotTrack_BeatsExplicitTelemetryFlag is the load-bearing
// claim: even when the operator passes --telemetry=on (which
// resolver step 3 would otherwise honor), DO_NOT_TRACK=1 short-circuits
// at step 2 and denies. This is the cross-cutting variant of
// resolve_test.go::TestResolve_DoNotTrack_Wins — the difference is
// we also assert the store remains UNCHANGED (no opportunistic
// "promote env decision to persisted" write).
func TestMatrix_DoNotTrack_BeatsExplicitTelemetryFlag(t *testing.T) {
	withMatrixXDG(t)

	on := true
	d := consent.Resolve(context.Background(), consent.Inputs{
		Env:           consent.MapEnv(map[string]string{"DO_NOT_TRACK": "1"}),
		TelemetryFlag: &on,
	})
	if d.State != consent.StateDenied || d.DecisionSource != consent.SourceEnv {
		t.Fatalf("got %+v, want denied/env (DO_NOT_TRACK non-overridable)", d)
	}

	// The resolver is pure: nothing should have been written to disk.
	if got := loadMatrixDecision(t); got.State != consent.StateUnknown {
		t.Errorf("on-disk State = %q, want %q (Resolve must not write)",
			got.State, consent.StateUnknown)
	}
}

// TestMatrix_DoNotTrack_BeatsPersistedGranted seeds a granted decision
// through the real FileStore, then runs the resolver with
// DO_NOT_TRACK=1 in env and the persisted decision injected. The
// expected outcome is denied/env — the persisted layer must lose. This
// asserts the wiring contract: callers feed Persisted from disk, the
// resolver still short-circuits the env over it.
func TestMatrix_DoNotTrack_BeatsPersistedGranted(t *testing.T) {
	withMatrixXDG(t)

	persisted := consent.Decision{
		State:          consent.StateGranted,
		DecidedAt:      matrixFixedNow(),
		PromptVersion:  1,
		DecisionSource: consent.SourcePrompt,
	}
	seedMatrixDecision(t, persisted)

	// Verify the seed actually landed.
	if got := loadMatrixDecision(t); got.State != consent.StateGranted {
		t.Fatalf("seed failed: on-disk State = %q, want %q",
			got.State, consent.StateGranted)
	}

	d := consent.Resolve(context.Background(), consent.Inputs{
		Env:       consent.MapEnv(map[string]string{"DO_NOT_TRACK": "1"}),
		Persisted: persisted,
	})
	if d.State != consent.StateDenied || d.DecisionSource != consent.SourceEnv {
		t.Fatalf("got %+v, want denied/env (DO_NOT_TRACK beats persisted granted)", d)
	}

	// The persisted decision on disk is unchanged — the resolver does
	// not mutate the store. Future ops (e.g. unset DO_NOT_TRACK) would
	// resurface the granted state.
	if got := loadMatrixDecision(t); got.State != consent.StateGranted {
		t.Errorf("on-disk State = %q after DO_NOT_TRACK resolve, want %q (must not mutate)",
			got.State, consent.StateGranted)
	}
}

// TestMatrix_PromptVersionBump_RepromptOnFreshResolverInvocation:
// when the persisted decision carries a prompt_version that does NOT
// match the current schema, the resolver still returns the persisted
// state verbatim (it's not the resolver's job to detect staleness —
// the PROMPT layer does that). The cross-cutting assertion is that
// the persisted Decision survives the resolver round-trip with its
// stale PromptVersion intact, so the prompt layer can see the stale
// number and decide to re-prompt.
//
// This is the contract that lets `kit telemetry status` show the user
// "your decision was made under prompt v0; v1 disclosure exists" even
// after the resolver has run.
func TestMatrix_PromptVersionBump_RepromptsOnTTYRun(t *testing.T) {
	withMatrixXDG(t)

	stale := consent.Decision{
		State:          consent.StateGranted,
		DecidedAt:      matrixFixedNow(),
		PromptVersion:  0, // stale by construction (current is >= 1)
		DecisionSource: consent.SourcePrompt,
	}
	seedMatrixDecision(t, stale)

	// Pure resolver: persisted at PromptVersion=0 survives verbatim.
	d := consent.Resolve(context.Background(), consent.Inputs{
		Env:       consent.MapEnv(nil),
		Persisted: stale,
	})
	if d.PromptVersion != 0 {
		t.Errorf("resolver dropped stale PromptVersion: got %d, want 0", d.PromptVersion)
	}
	if d.State != consent.StateGranted {
		t.Errorf("resolver mangled stale State: got %q, want granted", d.State)
	}
	// The stale number is what triggers the prompt-layer re-prompt.
	// We assert it survives so the prompt layer's branch fires; the
	// actual re-prompt UX is covered in
	// cmd/kit/telemetry/prompt_test.go::TestPrompt_StalePromptVersionReprompts.
	if got := loadMatrixDecision(t); got.PromptVersion != 0 {
		t.Errorf("on-disk PromptVersion = %d, want 0 (round-trip integrity)",
			got.PromptVersion)
	}
}

// TestMatrix_PromptVersionEqual_NoReprompt is the inverse: when the
// persisted PromptVersion already matches the current schema, the
// resolver returns the persisted Decision verbatim (no DecisionSource
// rewrite, no DecidedAt re-stamp). The prompt layer reads this
// directly and short-circuits — no UI rendered.
//
// We exercise PromptVersion=1 here because the cmd/kit/telemetry
// package's PromptVersion constant is 1; the matrix package can't
// import cmd/* without an inversion, so we hard-code the current
// shipped value. If PromptVersion bumps, BOTH this test and the prompt
// test will need to track — that's intentional, the bump should fan
// out as a deliberate cross-track update.
func TestMatrix_PromptVersionEqual_NoReprompt(t *testing.T) {
	withMatrixXDG(t)

	fresh := consent.Decision{
		State:          consent.StateGranted,
		DecidedAt:      matrixFixedNow(),
		PromptVersion:  1, // matches current shipped PromptVersion
		DecisionSource: consent.SourcePrompt,
	}
	seedMatrixDecision(t, fresh)

	d := consent.Resolve(context.Background(), consent.Inputs{
		Env:       consent.MapEnv(nil),
		Persisted: fresh,
	})
	if d != fresh {
		t.Errorf("resolver mutated fresh persisted decision: got %+v, want %+v", d, fresh)
	}
}

// TestMatrix_KitTelemetryModeOff_ShortCircuitsBeforeAnythingElse: the
// resolver step-1 kill switch beats every layer below it. We stack
// every potentially-conflicting input — persisted granted, explicit
// --telemetry=on, KIT_TELEMETRY_CONSENT=granted — and the env wins
// every time. The decision_source must be SourceEnv (not SourceFlag
// or SourcePrompt) so the audit trail names the right cause.
func TestMatrix_KitTelemetryModeOff_ShortCircuitsBeforeAnythingElse(t *testing.T) {
	withMatrixXDG(t)

	on := true
	persisted := consent.Decision{
		State:          consent.StateGranted,
		DecidedAt:      matrixFixedNow(),
		PromptVersion:  1,
		DecisionSource: consent.SourcePrompt,
	}

	d := consent.Resolve(context.Background(), consent.Inputs{
		Env: consent.MapEnv(map[string]string{
			"KIT_TELEMETRY_MODE":    "off",
			"KIT_TELEMETRY_CONSENT": "granted",
			"DO_NOT_TRACK":          "1", // even this is beaten by step 1
		}),
		TelemetryFlag: &on,
		Persisted:     persisted,
	})

	if d.State != consent.StateDenied {
		t.Errorf("State = %q, want %q (step 1 short-circuits)",
			d.State, consent.StateDenied)
	}
	if d.DecisionSource != consent.SourceEnv {
		t.Errorf("DecisionSource = %q, want %q (must name env as the cause)",
			d.DecisionSource, consent.SourceEnv)
	}
}

// TestMatrix_AppPrefixModeOff_BeatsKitTelemetryMode asserts the app
// prefix wins when both env vars carry "off". This is a tighter
// version of resolve_test.go::TestResolve_AppModeBeatsKitMode (which
// proves either being "off" wins) — here we want explicit ordering:
// when an adopter sets SPACED_TELEMETRY_MODE=off, the resolver
// terminates BEFORE consulting KIT_TELEMETRY_MODE. The visible
// difference is none (both produce denied/env) but the test pins the
// step-1 walk order, which matters if a future enhancement returns
// the offending var name in a diagnostic — the app prefix one would
// be the right one to surface.
//
// We can't directly assert "the resolver did not read KIT_TELEMETRY_MODE"
// without instrumenting the EnvProvider. We do that via a
// counter-backed env: SPACED is "off", KIT is a sentinel "should-not-
// read". A read of the sentinel value would still produce denied/env
// (because "off" matches anywhere), so we add a tracked map that
// records reads, and assert SPACED was read before KIT_TELEMETRY_MODE.
func TestMatrix_AppPrefixModeOff_BeatsKitTelemetryMode(t *testing.T) {
	withMatrixXDG(t)

	reads := make([]string, 0, 4)
	envMap := map[string]string{
		"SPACED_TELEMETRY_MODE": "off",
		"KIT_TELEMETRY_MODE":    "off",
	}
	env := func(k string) string {
		reads = append(reads, k)
		return envMap[k]
	}

	d := consent.Resolve(context.Background(), consent.Inputs{
		AppPrefix: "spaced",
		Env:       env,
	})
	if d.State != consent.StateDenied || d.DecisionSource != consent.SourceEnv {
		t.Fatalf("got %+v, want denied/env", d)
	}

	if len(reads) == 0 {
		t.Fatal("env was not read at all; resolver short-circuit pre-env? unexpected")
	}
	if reads[0] != "SPACED_TELEMETRY_MODE" {
		t.Errorf("first env read = %q, want %q (app prefix must precede kit prefix)",
			reads[0], "SPACED_TELEMETRY_MODE")
	}
	// And the kit prefix MUST NOT be consulted at all — step 1 returned
	// after the app prefix matched.
	for _, key := range reads {
		if key == "KIT_TELEMETRY_MODE" {
			t.Errorf("KIT_TELEMETRY_MODE was read after SPACED_TELEMETRY_MODE=off; resolver should have already returned")
		}
	}
}

// TestMatrix_ResolveAndStoreAgreeOnGranted is a sanity round-trip:
// persist granted via FileStore, feed the loaded Decision back through
// Resolve with a clean env, and assert the resolver returns the same
// bytes. This is the cross-cutting "does what we wrote match what we
// read?" check that no per-layer test covers — store_test only writes
// and reads through the same Store, resolve_test only resolves over
// synthetic Decisions.
func TestMatrix_ResolveAndStoreAgreeOnGranted(t *testing.T) {
	withMatrixXDG(t)

	want := consent.Decision{
		State:          consent.StateGranted,
		DecidedAt:      matrixFixedNow(),
		PromptVersion:  1,
		DecisionSource: consent.SourceFlag,
	}
	seedMatrixDecision(t, want)
	got := loadMatrixDecision(t)
	if got != want {
		t.Errorf("store round-trip drift:\n  got  %+v\n  want %+v", got, want)
	}

	resolved := consent.Resolve(context.Background(), consent.Inputs{
		Env:       consent.MapEnv(nil),
		Persisted: got,
	})
	if resolved != want {
		t.Errorf("resolver drift over persisted granted:\n  got  %+v\n  want %+v", resolved, want)
	}
}
