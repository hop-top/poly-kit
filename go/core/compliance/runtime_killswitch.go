package compliance

// rtConsentingTelemetryKillSwitch — F13 runtime sub-checks (c) + (d)
// from ADR-0037 and plan T-0701:
//
//   (c) DO_NOT_TRACK=1 suppresses emission even with granted consent
//   (d) <APP>_TELEMETRY_MODE=off (or KIT_TELEMETRY_MODE=off) suppresses
//       emission even with granted consent
//
// Methodology (per T-0701, each step in its own rtEnv):
//
//   1. baseline — seed consent=granted, run; assert >=1 event. Without
//      this sanity check, steps 2/3 are vacuously true (no events
//      because emission is broken, not because the env masks it).
//   2. DO_NOT_TRACK=1 — seed granted, set DO_NOT_TRACK=1, run;
//      assert no events.
//   3. <mode>=off — seed granted, set whichever mode env the toolspec
//      declares in kill_switch_envs (KIT_TELEMETRY_MODE or
//      <APP>_TELEMETRY_MODE per T-0739 reconciliation), value "off",
//      run; assert no events.
//
// The check skips when the toolspec is not opt-in (mirrors the static
// check). It assumes the spec is well-formed — T-0699's static check
// catches missing kill_switch_envs entries before this runtime check
// would run them.
//
// Harness caveat (per T-0700): the bus pkg does not auto-route from
// KIT_BUS_SINK=jsonl yet. Real adopter binaries must plumb the env
// into their bus builder OR route to a JSONLSink at
// KIT_BUS_SINK_PATH. The stub binary under
// `testdata/stub-telemetry-binary/` honors KIT_BUS_SINK_PATH directly
// so the tests can exercise the check end-to-end. Until adopter
// binaries gain the same wiring, this check is consumed only from
// tests (not from runtime.go's runRuntimeChecks); see plan T-0701
// for the production-integration step.

import (
	"context"
	"fmt"
	"time"
)

// rtKillSwitchPollBudget bounds the wait-for-emission window. 500ms
// is the T-0701 plan target — long enough for a normal adopter binary
// to flush, short enough not to wedge the test suite when the binary
// is mute by design.
const rtKillSwitchPollBudget = 500 * time.Millisecond

// rtKillSwitchRunTimeout bounds each child invocation. The check
// spawns the binary up to three times per call (baseline +
// DO_NOT_TRACK + mode); a hung child would otherwise block the
// runtime-check phase indefinitely.
const rtKillSwitchRunTimeout = 5 * time.Second

// rtConsentingTelemetryKillSwitch verifies ADR-0037 sub-conditions
// (c) and (d). envFactory builds a fresh rtEnv per scenario (each
// scenario gets a fresh tmpdir to avoid first-run state bleed); bin
// is the adopter binary path; spec drives which mode env to toggle
// and which read command to invoke.
//
// Returns a single CheckResult per ADR-0037's "one row per factor"
// model — multi-step failures are concatenated into Details.
//
// Production callers pass a closure that wraps newRTEnvDir over an
// os.MkdirTemp; tests pass `func() *rtEnv { return newRTEnv(t) }`.
func rtConsentingTelemetryKillSwitch(ctx context.Context, bin string, spec *toolspecYAML, envFactory func() *rtEnv) CheckResult {
	f := FactorConsentingTelemetry

	if !telemetryOptedIn(spec) {
		return skip(f, "binary does not opt into telemetry")
	}

	readCmd := findReadCommand(spec)
	if readCmd == "" {
		return skip(f, "no read command available to exercise emission")
	}

	modeEnv := pickModeEnv(spec.Telemetry.KillSwitchEnvs)
	if modeEnv == "" {
		// T-0699 should have caught this at static time; if we reach
		// here the spec is malformed for runtime purposes.
		return fail(f,
			"no <APP>_TELEMETRY_MODE-shaped entry in telemetry.kill_switch_envs",
			"Declare KIT_TELEMETRY_MODE or <APP>_TELEMETRY_MODE in kill_switch_envs")
	}

	// Step 1 — harness sanity baseline. If this scenario emits zero
	// events, steps 2 + 3 are meaningless (vacuously true). Per the
	// T-0700 harness caveat, the binary must route emission to
	// KIT_BUS_SINK_PATH; if it doesn't, this baseline catches it.
	if res, ok := killSwitchBaseline(ctx, envFactory, bin, readCmd); !ok {
		return res
	}

	// Step 2 — DO_NOT_TRACK=1 must mask emission even with granted
	// consent.
	if res, ok := killSwitchAssertSuppressed(
		ctx, envFactory, bin, readCmd,
		map[string]string{"DO_NOT_TRACK": "1"},
		"DO_NOT_TRACK=1",
	); !ok {
		return res
	}

	// Step 3 — declared mode env =off must mask emission even with
	// granted consent.
	if res, ok := killSwitchAssertSuppressed(
		ctx, envFactory, bin, readCmd,
		map[string]string{modeEnv: "off"},
		modeEnv+"=off",
	); !ok {
		return res
	}

	return pass(f, fmt.Sprintf(
		"DO_NOT_TRACK + %s=off both suppress emission with granted consent",
		modeEnv))
}

// killSwitchBaseline confirms emission works under granted consent
// with no kill-switch env set. Returns (zero, true) on success;
// (failure CheckResult, false) when the binary emitted nothing —
// indicating the harness or binary is broken, NOT that the kill
// switch worked.
func killSwitchBaseline(ctx context.Context, envFactory func() *rtEnv, bin, readCmd string) (CheckResult, bool) {
	f := FactorConsentingTelemetry
	e := envFactory()
	if err := e.SeedConsent("granted", "prompt", 1); err != nil {
		return fail(f,
			fmt.Sprintf("baseline: seed granted consent: %v", err),
			"Investigate file-system access in the test environment"), false
	}

	runCtx, cancel := context.WithTimeout(ctx, rtKillSwitchRunTimeout)
	defer cancel()
	_, _, _ = e.Run(runCtx, bin, readCmd, "--format", "json")

	evs, ok := e.PollEvents(1, rtKillSwitchPollBudget)
	if !ok || len(evs) == 0 {
		return fail(f,
			"harness sanity check: expected >=1 event in baseline run with granted consent, "+
				"but BusFile is empty. Either the binary does not honor KIT_BUS_SINK_PATH "+
				"(see ADR-0037 sub-condition c/d wiring) or emission is broken for an unrelated reason",
			"Plumb KIT_BUS_SINK_PATH into the adopter's bus builder so runtime checks can observe events"), false
	}
	return CheckResult{}, true
}

// killSwitchAssertSuppressed seeds granted consent, applies envs,
// runs the read command, and asserts no events were emitted. Returns
// (zero, true) on success; (failure CheckResult, false) when events
// leaked through.
func killSwitchAssertSuppressed(
	ctx context.Context,
	envFactory func() *rtEnv,
	bin, readCmd string,
	envs map[string]string,
	label string,
) (CheckResult, bool) {
	f := FactorConsentingTelemetry
	e := envFactory()
	if err := e.SeedConsent("granted", "prompt", 1); err != nil {
		return fail(f,
			fmt.Sprintf("%s: seed granted consent: %v", label, err),
			"Investigate file-system access in the test environment"), false
	}
	for k, v := range envs {
		e.SetEnv(k, v)
	}

	runCtx, cancel := context.WithTimeout(ctx, rtKillSwitchRunTimeout)
	defer cancel()
	_, _, _ = e.Run(runCtx, bin, readCmd, "--format", "json")

	// Use the full poll budget — we want to give a misbehaving binary
	// every chance to leak before declaring victory.
	evs, _ := e.PollEvents(1, rtKillSwitchPollBudget)
	if len(evs) > 0 {
		return fail(f,
			fmt.Sprintf("with granted consent + %s, expected 0 events but observed %d "+
				"(leaked through kill switch)", label, len(evs)),
			"Verify telemetry.Emitter.shouldEmit() honors DO_NOT_TRACK and "+
				"<APP>_TELEMETRY_MODE per ADR-0037 sub-conditions (c)+(d)"), false
	}
	return CheckResult{}, true
}

// pickModeEnv selects the first kill_switch_envs entry matching the
// <APP>_TELEMETRY_MODE shape. KIT_TELEMETRY_MODE matches by
// construction (the regex covers both kit and app-prefixed shapes).
// DO_NOT_TRACK is filtered out — it is the per-(c) env, not the
// per-(d) mode env. Returns "" when no match is found.
func pickModeEnv(envs []string) string {
	for _, e := range envs {
		if e == "DO_NOT_TRACK" {
			continue
		}
		if telemetryModeEnvShape.MatchString(e) {
			return e
		}
	}
	return ""
}
