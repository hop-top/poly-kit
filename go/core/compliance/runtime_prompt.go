package compliance

// rtConsentingTelemetryPrompt — F13 runtime sub-check (e) + (f):
//
//   (e) First-run prompt fires-or-skips per precedence.
//   (f) Persisted decision carries the canonical `prompt_version` field
//       (NOT `consent_version` or any other alias) AND a valid
//       decision_source (env | flag | prompt | config).
//
// Methodology (each scenario in its own rtEnv):
//
//   1. Fresh HOME + DO_NOT_TRACK=1 + any read cmd
//      → config persisted with state=denied, decision_source=env,
//        prompt_version set.
//   2. Fresh HOME + no env + any read cmd (non-TTY by construction)
//      → config persisted with state=denied, decision_source=config,
//        prompt_version set. (non-TTY default.)
//   3. Pre-seed config telemetry=granted + DO_NOT_TRACK=1 + any cmd
//      → no events emitted (env beats persisted). The persisted file
//        may or may not be re-written; we only assert the env-mask
//        behavior of step (c) here, leaving the rewrite policy to the
//        consent implementation.
//
// Scenario 4 (TTY prompt fires) is NOT testable from exec.Command —
// every subprocess we spawn is non-TTY by construction. Documented in
// the pass-path Details as "TTY prompt covered by manual review".
//
// Harness caveat: the bus pkg does not auto-route from
// KIT_BUS_SINK=jsonl yet. The stub binary under
// testdata/stub-telemetry-binary-prompt/ honors KIT_BUS_SINK_PATH AND
// writes the telemetry consent YAML directly under <XDG_CONFIG_HOME>/kit/
// so the harness can read it back deterministically.
//
// The canonical consent path is <XDG_CONFIG_HOME>/kit/config.yaml under
// kit.telemetry.consent. A legacy <XDG_CONFIG_HOME>/kit/telemetry.yaml
// layout (bare telemetry.consent) is read as a fallback so adopter
// binaries that still emit the pre-migration shape pass the runtime
// check.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// rtPromptRunTimeout bounds each child invocation. The check spawns
// the binary up to three times per call (scenarios 1 + 2 + 3); a hung
// child would otherwise block the runtime-check phase indefinitely.
const rtPromptRunTimeout = 5 * time.Second

// rtPromptPollBudget bounds the wait window for scenario 3's
// no-events assertion. Mirrors rtKillSwitchPollBudget — long enough
// for a normal adopter binary to flush, short enough not to wedge the
// suite when the binary is mute by design.
const rtPromptPollBudget = 500 * time.Millisecond

// validDecisionSources is the canonical set of decision_source values.
// A persisted decision whose decision_source is outside this set fails
// sub-condition (f) — the field exists for auditability ("why am I in
// this state") and an unknown value is a contract violation.
var validDecisionSources = map[string]struct{}{
	"env":    {},
	"flag":   {},
	"prompt": {},
	"config": {},
}

// persistedConsent mirrors the YAML shape of
// <XDG_CONFIG_HOME>/kit/config.yaml emitted by kit-consent. Only the
// fields the check needs are unmarshalled; extras are ignored. The
// canonical shape nests under `kit.telemetry.consent`; a legacy bare
// `telemetry.consent` layout (pre-config.yaml migration) is read via
// persistedConsentLegacy as a fallback.
type persistedConsent struct {
	Kit struct {
		Telemetry struct {
			Consent consentBlock `yaml:"consent"`
		} `yaml:"telemetry"`
	} `yaml:"kit"`
}

// persistedConsentLegacy mirrors the pre-refactor shape (bare
// telemetry.consent at the top level) stored in
// <XDG_CONFIG_HOME>/kit/telemetry.yaml.
type persistedConsentLegacy struct {
	Telemetry struct {
		Consent consentBlock `yaml:"consent"`
	} `yaml:"telemetry"`
}

type consentBlock struct {
	State          string `yaml:"state"`
	DecidedAt      string `yaml:"decided_at"`
	PromptVersion  *int   `yaml:"prompt_version"`
	DecisionSource string `yaml:"decision_source"`
}

// rtConsentingTelemetryPrompt verifies sub-conditions (e) + (f).
// envFactory builds a fresh rtEnv per scenario (each scenario
// gets a fresh tmpdir to avoid first-run state bleed); bin is the
// adopter binary path; spec drives the read-command pick and the
// expected prompt_version stamp.
//
// Returns a single CheckResult per the "one row per factor" model —
// multi-step failures are concatenated into Details. The
// signature mirrors rtConsentingTelemetryInspect / KillSwitch so all
// three runtime sub-checks can be invoked from the same site.
func rtConsentingTelemetryPrompt(ctx context.Context, bin string, spec *toolspecYAML, envFactory func() *rtEnv) CheckResult {
	f := FactorConsentingTelemetry

	if !telemetryOptedIn(spec) {
		return skip(f, "binary does not opt into telemetry")
	}

	readCmd := findReadCommand(spec)
	if readCmd == "" {
		return skip(f, "no read command available to exercise prompt precedence")
	}

	// Scenario 1: DO_NOT_TRACK=1 → state=denied, decision_source=env.
	if res, ok := promptAssertPersisted(
		ctx, envFactory, bin, readCmd,
		map[string]string{"DO_NOT_TRACK": "1"},
		"denied", "env",
		"scenario 1 (DO_NOT_TRACK=1)",
	); !ok {
		return res
	}

	// Scenario 2: no env, non-TTY → state=denied, decision_source=config.
	if res, ok := promptAssertPersisted(
		ctx, envFactory, bin, readCmd,
		nil,
		"denied", "config",
		"scenario 2 (non-TTY default)",
	); !ok {
		return res
	}

	// Scenario 3: pre-seed granted + DO_NOT_TRACK=1 → no events
	// emitted. Persisted file may or may not be rewritten — we
	// document the env-mask behavior only.
	if res, ok := promptAssertEnvBeatsGranted(
		ctx, envFactory, bin, readCmd,
		"scenario 3 (DO_NOT_TRACK beats persisted granted)",
	); !ok {
		return res
	}

	return pass(f, "first-run prompt precedence honored "+
		"(DO_NOT_TRACK→denied/env, non-TTY→denied/config, "+
		"env-beats-persisted-granted); TTY prompt covered by manual review")
}

// promptAssertPersisted seeds a fresh rtEnv, applies envs, runs the
// read command, and asserts the persisted consent file matches the
// expected state + decision_source + has a non-nil prompt_version.
// Returns (zero, true) on success; (failure CheckResult, false) when
// any assertion fails.
func promptAssertPersisted(
	ctx context.Context,
	envFactory func() *rtEnv,
	bin, readCmd string,
	envs map[string]string,
	wantState, wantSource, label string,
) (CheckResult, bool) {
	f := FactorConsentingTelemetry
	e := envFactory()
	for k, v := range envs {
		e.SetEnv(k, v)
	}

	runCtx, cancel := context.WithTimeout(ctx, rtPromptRunTimeout)
	defer cancel()
	_, _, _ = e.Run(runCtx, bin, readCmd, "--format", "json")

	got, err := readPersistedConsent(e.XDGConfig)
	if err != nil {
		return fail(f,
			fmt.Sprintf("%s: read persisted consent: %v", label, err),
			"Ensure the adopter binary persists a decision at "+
				"<XDG_CONFIG_HOME>/kit/config.yaml (kit.telemetry.consent) "+
				"on every invocation"), false
	}

	if got.State != wantState {
		return fail(f,
			fmt.Sprintf("%s: persisted state=%q, want %q",
				label, got.State, wantState),
			"Verify the consent resolver writes state="+wantState+
				" for this precedence step"), false
	}
	if got.DecisionSource != wantSource {
		return fail(f,
			fmt.Sprintf("%s: persisted decision_source=%q, want %q (valid: env|flag|prompt|config)",
				label, got.DecisionSource, wantSource),
			"Verify the consent resolver stamps decision_source="+wantSource+
				" for this precedence step"), false
	}
	if _, ok := validDecisionSources[got.DecisionSource]; !ok {
		return fail(f,
			fmt.Sprintf("%s: persisted decision_source=%q is not in canonical set {env, flag, prompt, config}",
				label, got.DecisionSource),
			"Use one of the canonical decision_source values"), false
	}
	// Sub-condition (f) field-name lock: prompt_version must be
	// present in the persisted file under the literal key
	// `prompt_version`. yaml.Unmarshal into a *int distinguishes
	// "missing" (nil pointer) from "present with value 0".
	if got.PromptVersion == nil {
		return fail(f,
			fmt.Sprintf("%s: persisted consent missing `prompt_version` field "+
				"(field-name lock — aliases like `consent_version` are rejected)",
				label),
			"Stamp `prompt_version` on every persisted decision"), false
	}

	return CheckResult{}, true
}

// promptAssertEnvBeatsGranted seeds the consent file with
// state=granted, applies DO_NOT_TRACK=1, runs the read command, and
// asserts zero telemetry events were emitted. The persisted file may
// or may not be rewritten — we deliberately do NOT assert its
// post-run shape, since rewrite policy under env override is left to
// the consent implementation.
func promptAssertEnvBeatsGranted(
	ctx context.Context,
	envFactory func() *rtEnv,
	bin, readCmd, label string,
) (CheckResult, bool) {
	f := FactorConsentingTelemetry
	e := envFactory()
	if err := e.SeedConsent("granted", "prompt", 1); err != nil {
		return fail(f,
			fmt.Sprintf("%s: seed granted consent: %v", label, err),
			"Investigate file-system access in the test environment"), false
	}
	e.SetEnv("DO_NOT_TRACK", "1")

	runCtx, cancel := context.WithTimeout(ctx, rtPromptRunTimeout)
	defer cancel()
	_, _, _ = e.Run(runCtx, bin, readCmd, "--format", "json")

	// Use the full poll budget — we want to give a misbehaving binary
	// every chance to leak before declaring victory.
	evs, _ := e.PollEvents(1, rtPromptPollBudget)
	if len(evs) > 0 {
		return fail(f,
			fmt.Sprintf("%s: expected 0 events (env beats persisted) but observed %d",
				label, len(evs)),
			"Ensure DO_NOT_TRACK=1 short-circuits emission even when the "+
				"persisted state is granted"), false
	}

	return CheckResult{}, true
}

// readPersistedConsent reads the persisted consent decision. It prefers
// the canonical <xdgConfig>/kit/config.yaml (kit.telemetry.consent) and
// falls back to the legacy <xdgConfig>/kit/telemetry.yaml (bare
// telemetry.consent) layout when the canonical file is absent or
// missing the consent block.
//
// The error path distinguishes "no file at either location" (returned
// as a concrete error so the caller's Details message says "no consent
// file persisted") from "malformed yaml".
func readPersistedConsent(xdgConfig string) (*consentBlock, error) {
	canonical := filepath.Join(xdgConfig, "kit", "config.yaml")
	legacy := filepath.Join(xdgConfig, "kit", "telemetry.yaml")

	raw, err := os.ReadFile(canonical)
	if err == nil {
		if hasConsentVersionAlias(raw) && !hasPromptVersionField(raw) {
			return nil, fmt.Errorf("persisted consent uses `consent_version` alias "+
				"(field-name lock: only `prompt_version` is accepted) at %s", canonical)
		}
		var pc persistedConsent
		if err := yaml.Unmarshal(raw, &pc); err != nil {
			return nil, fmt.Errorf("parse %s: %w", canonical, err)
		}
		// If the canonical file is present but the kit.telemetry.consent
		// block is empty (zero state), fall through to legacy so an
		// adopter binary that wrote only config.yaml siblings without
		// the consent block is not silently green-lit.
		if pc.Kit.Telemetry.Consent.State != "" {
			return &pc.Kit.Telemetry.Consent, nil
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read %s: %w", canonical, err)
	}

	rawLegacy, err := os.ReadFile(legacy)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no consent file at %s or %s — binary did not persist a decision",
				canonical, legacy)
		}
		return nil, fmt.Errorf("read %s: %w", legacy, err)
	}
	// Field-name lock cross-check on the legacy file too.
	if hasConsentVersionAlias(rawLegacy) && !hasPromptVersionField(rawLegacy) {
		return nil, fmt.Errorf("persisted consent uses `consent_version` alias "+
			"(field-name lock: only `prompt_version` is accepted) at %s", legacy)
	}
	var pcLegacy persistedConsentLegacy
	if err := yaml.Unmarshal(rawLegacy, &pcLegacy); err != nil {
		return nil, fmt.Errorf("parse %s: %w", legacy, err)
	}
	return &pcLegacy.Telemetry.Consent, nil
}

// hasPromptVersionField is a string-level scan to ensure the literal
// key `prompt_version` is present in the YAML source. yaml.Unmarshal
// into a typed struct already requires the canonical key, but a
// belt-and-braces literal check guards against future struct-tag
// changes accidentally accepting an alias.
func hasPromptVersionField(raw []byte) bool {
	return strings.Contains(string(raw), "prompt_version:")
}

// hasConsentVersionAlias detects the stale `consent_version` alias
// that is explicitly rejected in favor of `prompt_version`. Used to
// produce a pointed error message rather than a generic "missing
// field" failure.
func hasConsentVersionAlias(raw []byte) bool {
	return strings.Contains(string(raw), "consent_version:")
}
