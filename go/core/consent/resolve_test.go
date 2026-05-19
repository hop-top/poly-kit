package consent

import (
	"context"
	"testing"
	"time"
)

// boolPtr is a one-liner helper for setting TelemetryFlag in table rows.
func boolPtr(b bool) *bool { return &b }

// fixedPersisted is reused across tests that want to assert pass-through
// preservation of decided_at / prompt_version / decision_source.
var fixedPersisted = Decision{
	State:          StateGranted,
	DecidedAt:      time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC),
	PromptVersion:  2,
	DecisionSource: SourcePrompt,
}

func TestResolve_Default_DeniedWhenNothingSet(t *testing.T) {
	d := Resolve(context.Background(), Inputs{Env: MapEnv(nil)})
	if d.State != StateDenied {
		t.Fatalf("State = %q, want %q", d.State, StateDenied)
	}
	if d.DecisionSource != SourceConfig {
		t.Fatalf("DecisionSource = %q, want %q (default branch is config-sourced)", d.DecisionSource, SourceConfig)
	}
}

func TestResolve_AppPrefixModeOff_Wins(t *testing.T) {
	in := Inputs{
		AppPrefix: "spaced",
		Env: MapEnv(map[string]string{
			"SPACED_TELEMETRY_MODE": "off",
			"KIT_TELEMETRY_CONSENT": "granted", // would otherwise win step 5
			"DO_NOT_TRACK":          "1",       // would otherwise win step 2
		}),
		TelemetryFlag: boolPtr(true), // would otherwise win step 3
	}
	d := Resolve(context.Background(), in)
	if d.State != StateDenied || d.DecisionSource != SourceEnv {
		t.Fatalf("got %+v, want denied/env (app prefix mode=off short-circuits)", d)
	}
}

func TestResolve_KitModeOff_Wins(t *testing.T) {
	in := Inputs{
		Env: MapEnv(map[string]string{
			"KIT_TELEMETRY_MODE":    "off",
			"KIT_TELEMETRY_CONSENT": "granted",
		}),
		TelemetryFlag: boolPtr(true),
	}
	d := Resolve(context.Background(), in)
	if d.State != StateDenied || d.DecisionSource != SourceEnv {
		t.Fatalf("got %+v, want denied/env", d)
	}
}

// Per ADR-0036 section 5: ONLY mode=off short-circuits. Non-"off" mode
// values (anon/full/empty) are consumed by kit-telemetry's CurrentMode,
// not by this resolver. So app=anon + kit=off -> step 1 sees kit=off
// and short-circuits to denied.
func TestResolve_AppModeBeatsKitMode(t *testing.T) {
	// Case A: app=anon (not "off"), kit=off. Step 1 walks app first
	// (no short-circuit because "anon" != "off"), then kit (off) ->
	// short-circuit to denied.
	in := Inputs{
		AppPrefix: "spaced",
		Env: MapEnv(map[string]string{
			"SPACED_TELEMETRY_MODE": "anon",
			"KIT_TELEMETRY_MODE":    "off",
		}),
	}
	d := Resolve(context.Background(), in)
	if d.State != StateDenied || d.DecisionSource != SourceEnv {
		t.Fatalf("anon+kit=off: got %+v, want denied/env (kit=off short-circuits)", d)
	}

	// Case B: app=off, kit=anon. App prefix is checked FIRST and
	// short-circuits before the kit prefix is read.
	in2 := Inputs{
		AppPrefix: "spaced",
		Env: MapEnv(map[string]string{
			"SPACED_TELEMETRY_MODE": "off",
			"KIT_TELEMETRY_MODE":    "anon",
		}),
		TelemetryFlag: boolPtr(true),
	}
	d2 := Resolve(context.Background(), in2)
	if d2.State != StateDenied || d2.DecisionSource != SourceEnv {
		t.Fatalf("app=off+kit=anon: got %+v, want denied/env (app prefix short-circuits)", d2)
	}
}

func TestResolve_DoNotTrack_Wins(t *testing.T) {
	in := Inputs{
		Env:           MapEnv(map[string]string{"DO_NOT_TRACK": "1"}),
		TelemetryFlag: boolPtr(true), // explicit on; DO_NOT_TRACK still wins
	}
	d := Resolve(context.Background(), in)
	if d.State != StateDenied || d.DecisionSource != SourceEnv {
		t.Fatalf("got %+v, want denied/env (DO_NOT_TRACK non-overridable)", d)
	}
}

func TestResolve_DoNotTrack_BeatsPersistedGranted(t *testing.T) {
	in := Inputs{
		Env:       MapEnv(map[string]string{"DO_NOT_TRACK": "1"}),
		Persisted: fixedPersisted, // granted on disk
	}
	d := Resolve(context.Background(), in)
	if d.State != StateDenied || d.DecisionSource != SourceEnv {
		t.Fatalf("got %+v, want denied/env", d)
	}
}

// DO_NOT_TRACK with any non-empty value (other than "0"/"false")
// triggers the env opt-out. We honor consoledonottrack.com literally:
// "the value should not matter". The two explicit "do-track" tokens
// ("0" and "false") fall through to the rest of the precedence chain
// so a user can disable DO_NOT_TRACK per-invocation without unset.
// See consent.DoNotTrackEnabled for the canonical predicate.
func TestResolve_DoNotTrack_BroadValues_OptOut(t *testing.T) {
	for _, v := range []string{"true", "yes", "anything"} {
		v := v
		t.Run(v, func(t *testing.T) {
			in := Inputs{
				Env:       MapEnv(map[string]string{"DO_NOT_TRACK": v}),
				Persisted: fixedPersisted,
			}
			d := Resolve(context.Background(), in)
			if d.State != StateDenied || d.DecisionSource != SourceEnv {
				t.Fatalf("DO_NOT_TRACK=%q: got %+v, want denied/env", v, d)
			}
		})
	}
}

// "0" and "false" are the documented "explicit do-track" tokens: they
// short-circuit the DO_NOT_TRACK rail without triggering it. Persisted
// granted survives.
func TestResolve_DoNotTrack_ExplicitOff_Ignored(t *testing.T) {
	for _, v := range []string{"0", "false", "FALSE"} {
		v := v
		t.Run(v, func(t *testing.T) {
			in := Inputs{
				Env:       MapEnv(map[string]string{"DO_NOT_TRACK": v}),
				Persisted: fixedPersisted,
			}
			d := Resolve(context.Background(), in)
			if d.State != StateGranted {
				t.Fatalf("DO_NOT_TRACK=%q: State = %q, want granted (explicit do-track tokens fall through)", v, d.State)
			}
		})
	}
}

func TestResolve_TelemetryFlagOn(t *testing.T) {
	in := Inputs{
		Env:           MapEnv(nil),
		TelemetryFlag: boolPtr(true),
	}
	d := Resolve(context.Background(), in)
	if d.State != StateGranted || d.DecisionSource != SourceFlag {
		t.Fatalf("got %+v, want granted/flag", d)
	}
}

func TestResolve_TelemetryFlagOff(t *testing.T) {
	in := Inputs{
		Env:           MapEnv(map[string]string{"KIT_TELEMETRY_CONSENT": "granted"}),
		TelemetryFlag: boolPtr(false),
	}
	d := Resolve(context.Background(), in)
	if d.State != StateDenied || d.DecisionSource != SourceFlag {
		t.Fatalf("got %+v, want denied/flag (flag beats env)", d)
	}
}

func TestResolve_YesAlone_NoEffect(t *testing.T) {
	in := Inputs{
		Env:     MapEnv(map[string]string{"KIT_TELEMETRY_CONSENT": "granted"}),
		YesFlag: true,
		// TelemetryFlag deliberately nil — --yes alone should not grant
	}
	d := Resolve(context.Background(), in)
	// Falls through to step 5: KIT_TELEMETRY_CONSENT=granted.
	if d.State != StateGranted || d.DecisionSource != SourceEnv {
		t.Fatalf("got %+v, want granted/env (yes alone falls through to env layer)", d)
	}
}

// --yes paired with --telemetry=on is just the flag path; --yes adds
// no elevation (it's the non-interactive confirmation hint, not a
// grant signal).
func TestResolve_YesWithFlagOn_Granted(t *testing.T) {
	in := Inputs{
		Env:           MapEnv(nil),
		TelemetryFlag: boolPtr(true),
		YesFlag:       true,
	}
	d := Resolve(context.Background(), in)
	if d.State != StateGranted || d.DecisionSource != SourceFlag {
		t.Fatalf("got %+v, want granted/flag", d)
	}
}

func TestResolve_EnvConsent_GrantedAccepted(t *testing.T) {
	in := Inputs{
		Env: MapEnv(map[string]string{"KIT_TELEMETRY_CONSENT": "granted"}),
	}
	d := Resolve(context.Background(), in)
	if d.State != StateGranted || d.DecisionSource != SourceEnv {
		t.Fatalf("got %+v, want granted/env", d)
	}
}

func TestResolve_EnvConsent_DeniedAccepted(t *testing.T) {
	in := Inputs{
		Env:       MapEnv(map[string]string{"KIT_TELEMETRY_CONSENT": "denied"}),
		Persisted: fixedPersisted, // would otherwise be granted
	}
	d := Resolve(context.Background(), in)
	if d.State != StateDenied || d.DecisionSource != SourceEnv {
		t.Fatalf("got %+v, want denied/env (env denial beats persisted grant)", d)
	}
}

func TestResolve_EnvConsent_LegacyValueRejectedWithDiagnostic(t *testing.T) {
	in := Inputs{
		Env: MapEnv(map[string]string{"KIT_TELEMETRY_CONSENT": "allow"}),
	}
	d, diags := ResolveWithDiagnostics(context.Background(), in)
	// Falls through to default-denied.
	if d.State != StateDenied || d.DecisionSource != SourceConfig {
		t.Fatalf("got %+v, want denied/config", d)
	}
	if len(diags) != 1 {
		t.Fatalf("len(diags) = %d, want 1; diags = %+v", len(diags), diags)
	}
	if diags[0].Var != "KIT_TELEMETRY_CONSENT" || diags[0].Value != "allow" {
		t.Fatalf("diag = %+v, want Var=KIT_TELEMETRY_CONSENT, Value=allow", diags[0])
	}
	// Error() produces a useful message.
	if got := diags[0].Error(); got == "" {
		t.Fatal("Error() returned empty string")
	}
}

func TestResolve_EnvConsent_GarbageRejectedWithDiagnostic(t *testing.T) {
	in := Inputs{
		Env: MapEnv(map[string]string{"KIT_TELEMETRY_CONSENT": "banana"}),
	}
	d, diags := ResolveWithDiagnostics(context.Background(), in)
	if d.State != StateDenied {
		t.Fatalf("State = %q, want denied", d.State)
	}
	if len(diags) != 1 {
		t.Fatalf("len(diags) = %d, want 1", len(diags))
	}
	if diags[0].Value != "banana" {
		t.Fatalf("diag.Value = %q, want banana", diags[0].Value)
	}
}

// Even with a bad env consent value, a persisted decision still wins
// the fall-through (the env is treated as unset).
func TestResolve_EnvConsent_LegacyFallsThroughToPersisted(t *testing.T) {
	in := Inputs{
		Env:       MapEnv(map[string]string{"KIT_TELEMETRY_CONSENT": "deny"}),
		Persisted: fixedPersisted, // granted on disk
	}
	d, diags := ResolveWithDiagnostics(context.Background(), in)
	if d.State != StateGranted {
		t.Fatalf("State = %q, want granted (env=deny is legacy; persisted wins)", d.State)
	}
	if len(diags) != 1 {
		t.Fatalf("len(diags) = %d, want 1", len(diags))
	}
}

func TestResolve_PersistedGranted_NoOverride(t *testing.T) {
	in := Inputs{
		Env:       MapEnv(nil),
		Persisted: fixedPersisted,
	}
	d := Resolve(context.Background(), in)
	if d.State != StateGranted {
		t.Fatalf("State = %q, want granted", d.State)
	}
	// All four fields must be preserved verbatim — the resolver is
	// returning the persisted decision unchanged.
	if !d.DecidedAt.Equal(fixedPersisted.DecidedAt) {
		t.Fatalf("DecidedAt = %v, want %v", d.DecidedAt, fixedPersisted.DecidedAt)
	}
	if d.PromptVersion != fixedPersisted.PromptVersion {
		t.Fatalf("PromptVersion = %d, want %d", d.PromptVersion, fixedPersisted.PromptVersion)
	}
	if d.DecisionSource != fixedPersisted.DecisionSource {
		t.Fatalf("DecisionSource = %q, want %q", d.DecisionSource, fixedPersisted.DecisionSource)
	}
}

func TestResolve_PersistedDenied(t *testing.T) {
	in := Inputs{
		Env: MapEnv(nil),
		Persisted: Decision{
			State:          StateDenied,
			DecidedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			PromptVersion:  1,
			DecisionSource: SourceFlag,
		},
	}
	d := Resolve(context.Background(), in)
	if d.State != StateDenied || d.DecisionSource != SourceFlag {
		t.Fatalf("got %+v, want denied/flag (persisted preserved)", d)
	}
}

// Persisted{State: ""} or {State: StateUnknown} both fall through to
// the default branch.
func TestResolve_PersistedUnknown_FallsThroughToDefault(t *testing.T) {
	cases := []struct {
		name      string
		persisted Decision
	}{
		{"zero", Decision{}},
		{"explicit_unknown", Decision{State: StateUnknown}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Resolve(context.Background(), Inputs{
				Env:       MapEnv(nil),
				Persisted: tc.persisted,
			})
			if d.State != StateDenied {
				t.Fatalf("State = %q, want denied", d.State)
			}
			if d.DecisionSource != SourceConfig {
				t.Fatalf("DecisionSource = %q, want %q (default branch)", d.DecisionSource, SourceConfig)
			}
		})
	}
}

// TestResolve_FullCascade: every layer set, assert highest wins, then
// flip one layer off at a time and assert the cascade walks down the
// precedence chain.
func TestResolve_FullCascade(t *testing.T) {
	// Layer-1 (KIT mode=off) set: wins regardless of everything below.
	base := Inputs{
		AppPrefix: "spaced",
		Env: MapEnv(map[string]string{
			"SPACED_TELEMETRY_MODE": "off",
			"KIT_TELEMETRY_MODE":    "off",
			"DO_NOT_TRACK":          "1",
			"KIT_TELEMETRY_CONSENT": "granted",
		}),
		TelemetryFlag: boolPtr(true),
		YesFlag:       true,
		Persisted:     fixedPersisted,
	}
	d := Resolve(context.Background(), base)
	if d.State != StateDenied || d.DecisionSource != SourceEnv {
		t.Fatalf("all-set: got %+v, want denied/env (mode=off)", d)
	}

	// Drop mode=off (both app and kit). DO_NOT_TRACK=1 takes over.
	base.Env = MapEnv(map[string]string{
		"DO_NOT_TRACK":          "1",
		"KIT_TELEMETRY_CONSENT": "granted",
	})
	d = Resolve(context.Background(), base)
	if d.State != StateDenied || d.DecisionSource != SourceEnv {
		t.Fatalf("no mode: got %+v, want denied/env (DO_NOT_TRACK)", d)
	}

	// Drop DO_NOT_TRACK. TelemetryFlag=*true takes over.
	base.Env = MapEnv(map[string]string{
		"KIT_TELEMETRY_CONSENT": "granted",
	})
	d = Resolve(context.Background(), base)
	if d.State != StateGranted || d.DecisionSource != SourceFlag {
		t.Fatalf("no DNT: got %+v, want granted/flag", d)
	}

	// Drop TelemetryFlag. KIT_TELEMETRY_CONSENT=granted takes over.
	base.TelemetryFlag = nil
	d = Resolve(context.Background(), base)
	if d.State != StateGranted || d.DecisionSource != SourceEnv {
		t.Fatalf("no flag: got %+v, want granted/env", d)
	}

	// Drop env consent. Persisted (granted) takes over.
	base.Env = MapEnv(nil)
	d = Resolve(context.Background(), base)
	if d.State != StateGranted || d.DecisionSource != SourcePrompt {
		t.Fatalf("no env consent: got %+v, want granted/prompt (persisted)", d)
	}

	// Drop persisted. Default-denied/config takes over.
	base.Persisted = Decision{}
	d = Resolve(context.Background(), base)
	if d.State != StateDenied || d.DecisionSource != SourceConfig {
		t.Fatalf("no persisted: got %+v, want denied/config (default)", d)
	}
}

// Resolve and ResolveWithDiagnostics must agree on the Decision —
// Resolve is just the diagnostics-discarding shorthand.
func TestResolve_AgreesWithDiagnostics(t *testing.T) {
	in := Inputs{
		Env:           MapEnv(map[string]string{"KIT_TELEMETRY_CONSENT": "granted"}),
		TelemetryFlag: boolPtr(false),
	}
	d1 := Resolve(context.Background(), in)
	d2, _ := ResolveWithDiagnostics(context.Background(), in)
	if d1 != d2 {
		t.Fatalf("Resolve = %+v; ResolveWithDiagnostics = %+v; should agree", d1, d2)
	}
}

// nil Env on Inputs falls back to OSEnv (no panic). We can't easily
// assert on os.Getenv values without t.Setenv polluting parallel tests;
// the assertion here is just that the call completes and returns a
// usable Decision.
func TestResolve_NilEnv_FallsBackToOSEnv(t *testing.T) {
	d := Resolve(context.Background(), Inputs{}) // Env: nil
	// With no overrides and no persisted state, default-denied wins.
	if d.State != StateDenied {
		t.Fatalf("State = %q, want denied (default)", d.State)
	}
}
