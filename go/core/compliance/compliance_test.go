package compliance_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"hop.top/kit/go/core/compliance"
)

func testdataPath() string {
	// Test runs from hops/main/go/core/compliance/; the toolspec lives
	// at hops/main/examples/spaced/spaced.toolspec.yaml → three levels up
	// to reach hops/main, then into examples/spaced.
	return filepath.Join("..", "..", "..", "examples", "spaced", "spaced.toolspec.yaml")
}

func TestRunStatic_SpacedToolspec(t *testing.T) {
	path := testdataPath()
	if _, err := os.Stat(path); err != nil {
		t.Skipf("toolspec not found: %s", path)
	}

	results, err := compliance.RunStatic(path)
	require.NoError(t, err)
	require.NotEmpty(t, results)

	byFactor := make(map[compliance.Factor]compliance.CheckResult)
	for _, r := range results {
		byFactor[r.Factor] = r
	}

	// Factor 1: Self-describing — commands non-empty
	assert.Equal(t, "pass", byFactor[compliance.FactorSelfDescribing].Status)

	// Factor 2: Structured I/O — at least one output_schema
	assert.Equal(t, "pass", byFactor[compliance.FactorStructuredIO].Status)

	// Factor 4: Contracts — mutating commands have contract fields
	assert.Equal(t, "pass", byFactor[compliance.FactorContractsErrors].Status)

	// Factor 5: Preview — mutating commands have preview_modes
	assert.Equal(t, "pass", byFactor[compliance.FactorPreview].Status)

	// Factor 6: Idempotency — contract.idempotent declared
	assert.Equal(t, "pass", byFactor[compliance.FactorIdempotency].Status)

	// Factor 7: State transparency — config_commands present
	assert.Equal(t, "pass", byFactor[compliance.FactorStateTransparency].Status)

	// Factor 8: Safe delegation — dangerous cmds have safety
	assert.Equal(t, "pass", byFactor[compliance.FactorSafeDelegation].Status)

	// Factor 11: Evolution — schema_version set
	assert.Equal(t, "pass", byFactor[compliance.FactorEvolution].Status)

	// Factor 12: Auth lifecycle — auth_commands present
	assert.Contains(t, []string{"pass", "skip"},
		byFactor[compliance.FactorAuthLifecycle].Status)

	// Skipped factors (runtime only)
	assert.Equal(t, "skip", byFactor[compliance.FactorProvenance].Status)
	assert.Equal(t, "skip", byFactor[compliance.FactorStreamDiscipline].Status)
	assert.Equal(t, "skip", byFactor[compliance.FactorObservableOps].Status)
}

func TestRunStatic_EmptySpec(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "empty.yaml")
	require.NoError(t, os.WriteFile(p, []byte("name: empty\n"), 0644))

	results, err := compliance.RunStatic(p)
	require.NoError(t, err)

	failing := 0
	for _, r := range results {
		if r.Status == "fail" {
			failing++
		}
	}
	assert.Greater(t, failing, 0, "empty spec should have failures")
}

func TestRun_ReturnsReport(t *testing.T) {
	path := testdataPath()
	if _, err := os.Stat(path); err != nil {
		t.Skipf("toolspec not found: %s", path)
	}

	// Static-only (no binary)
	report, err := compliance.Run("", path)
	require.NoError(t, err)
	require.NotNil(t, report)

	assert.Equal(t, path, report.Toolspec)
	assert.Equal(t, 12, report.Total)
	assert.GreaterOrEqual(t, report.Score, 1)
}

func TestFormatReport_Text(t *testing.T) {
	report := &compliance.Report{
		Binary:   "test-bin",
		Toolspec: "test.yaml",
		Total:    12,
		Score:    8,
		Results: []compliance.CheckResult{
			{
				Factor: compliance.FactorSelfDescribing,
				Name:   "Self-Describing",
				Status: "pass",
			},
			{
				Factor:     compliance.FactorStructuredIO,
				Name:       "Structured I/O",
				Status:     "fail",
				Suggestion: "Add output_schema",
			},
		},
	}

	out := compliance.FormatReport(report, "text")
	assert.Contains(t, out, "Self-Describing")
	assert.Contains(t, out, "PASS")
	assert.Contains(t, out, "FAIL")
	assert.Contains(t, out, "8/12")
}

func TestFormatReport_JSON(t *testing.T) {
	report := &compliance.Report{
		Binary:   "test-bin",
		Toolspec: "test.yaml",
		Total:    12,
		Score:    8,
		Results: []compliance.CheckResult{
			{
				Factor: compliance.FactorSelfDescribing,
				Name:   "Self-Describing",
				Status: "pass",
			},
		},
	}

	out := compliance.FormatReport(report, "json")
	assert.Contains(t, out, `"score"`)
	assert.Contains(t, out, `"total"`)
	assert.Contains(t, out, `"results"`)
}

func TestFactorNames(t *testing.T) {
	// Every factor should have a name
	for f := compliance.FactorSelfDescribing; f <= compliance.FactorConsentingTelemetry; f++ {
		assert.NotEmpty(t, f.String(), "factor %d should have name", f)
	}
	assert.Equal(t, "Consenting Telemetry",
		compliance.FactorConsentingTelemetry.String(),
		"F13 name must match ADR-0037 (load-bearing for format padding)")
}

// TestConsentingTelemetry_SkipsWhenNotOptedIn confirms ADR-0037's
// "skip not counted toward Total" semantics: a toolspec without a
// telemetry block produces F13=skip and the report Total stays at 12.
func TestConsentingTelemetry_SkipsWhenNotOptedIn(t *testing.T) {
	body := `name: probe
schema_version: "1"
commands:
  - name: ping
`
	p := writeToolspec(t, body)

	report, err := compliance.Run("", p)
	require.NoError(t, err)
	require.NotNil(t, report)

	var f13 *compliance.CheckResult
	for i := range report.Results {
		if report.Results[i].Factor == compliance.FactorConsentingTelemetry {
			f13 = &report.Results[i]
			break
		}
	}
	require.NotNil(t, f13, "F13 result must be present even when skipped")
	assert.Equal(t, "skip", f13.Status,
		"non-opt-in toolspec must yield F13=skip")
	assert.Equal(t, 12, report.Total,
		"skip results are excluded from Total per ADR-0037; "+
			"non-opt-in binaries score N/12 not N/13")
}

// TestConsentingTelemetry_RunsWhenOptedIn confirms an opt-in toolspec
// (enabled:true) with a fully well-formed telemetry block produces a
// pass result for F13 and bumps Total to 13. Updated by T-0699 to
// satisfy the real sub-condition checks (categories, consent_subcommands
// mapping to commands tree, kill_switch_envs, prompt_version,
// redact_rules) in addition to the opt-in gate.
func TestConsentingTelemetry_RunsWhenOptedIn(t *testing.T) {
	body := `name: probe
schema_version: "1"
commands:
  - name: ping
  - name: telemetry
    children:
      - name: status
      - name: enable
      - name: disable
      - name: reset
      - name: inspect
telemetry:
  enabled: true
  categories: [invocation]
  sinks: [bus]
  consent_command: "probe telemetry"
  consent_subcommands: [status, enable, disable, reset, inspect]
  kill_switch_envs: [DO_NOT_TRACK, PROBE_TELEMETRY_MODE]
  prompt_version: "v1"
  redact_rules: kit-default
`
	p := writeToolspec(t, body)

	report, err := compliance.Run("", p)
	require.NoError(t, err)
	require.NotNil(t, report)

	var f13 *compliance.CheckResult
	for i := range report.Results {
		if report.Results[i].Factor == compliance.FactorConsentingTelemetry {
			f13 = &report.Results[i]
			break
		}
	}
	require.NotNil(t, f13, "F13 result must be present when opted in")
	assert.Equal(t, "pass", f13.Status,
		"opt-in toolspec with well-formed telemetry must yield F13=pass; "+
			"details=%q", f13.Details)
	assert.Equal(t, 13, report.Total,
		"opt-in adds F13 to the denominator: 13 non-skip factors total")
}

// writeToolspec is a helper that writes raw YAML to a temp file and
// returns the path. The compliance package only exposes loadSpec via
// RunStatic, so telemetry-block round-trip tests reach the parsed
// struct by exercising RunStatic against a file fixture and then
// re-parsing the same YAML through the public surface where
// available. Where direct struct access is required, we mirror the
// fixture parse via yaml.Unmarshal against a local twin struct.
func writeToolspec(t *testing.T, body string) string {
	t.Helper()
	tmp := t.TempDir()
	p := filepath.Join(tmp, "toolspec.yaml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	return p
}

// telemetryProbe is a local twin of the package-private telemetryYAML
// used only by these tests to verify YAML field-tag wiring. The
// authoritative struct lives in compliance.go; if these tags drift,
// these tests will fail and surface the rename.
type telemetryProbe struct {
	Enabled            bool     `yaml:"enabled"`
	Categories         []string `yaml:"categories"`
	Sinks              []string `yaml:"sinks"`
	ConsentCommand     string   `yaml:"consent_command"`
	ConsentSubcommands []string `yaml:"consent_subcommands"`
	KillSwitchEnvs     []string `yaml:"kill_switch_envs"`
	PromptVersion      string   `yaml:"prompt_version"`
	RedactRules        string   `yaml:"redact_rules"`
}

type specProbe struct {
	Name      string          `yaml:"name"`
	Telemetry *telemetryProbe `yaml:"telemetry,omitempty"`
}

func parseSpecProbe(t *testing.T, body string) specProbe {
	t.Helper()
	var s specProbe
	require.NoError(t, yaml.Unmarshal([]byte(body), &s))
	return s
}

func TestToolspecTelemetryBlock_UnmarshalsAllFields(t *testing.T) {
	body := `name: probe
schema_version: "1"
telemetry:
  enabled: true
  categories: [invocation, error, lifecycle]
  sinks: [bus, jsonl]
  consent_command: "probe telemetry"
  consent_subcommands: [status, enable, disable, reset, inspect]
  kill_switch_envs: [DO_NOT_TRACK, PROBE_TELEMETRY_MODE]
  prompt_version: "v1"
  redact_rules: kit-default
`
	s := parseSpecProbe(t, body)
	require.NotNil(t, s.Telemetry)
	assert.True(t, s.Telemetry.Enabled)
	assert.Equal(t, []string{"invocation", "error", "lifecycle"}, s.Telemetry.Categories)
	assert.Equal(t, []string{"bus", "jsonl"}, s.Telemetry.Sinks)
	assert.Equal(t, "probe telemetry", s.Telemetry.ConsentCommand)
	assert.Equal(t,
		[]string{"status", "enable", "disable", "reset", "inspect"},
		s.Telemetry.ConsentSubcommands)
	assert.Equal(t,
		[]string{"DO_NOT_TRACK", "PROBE_TELEMETRY_MODE"},
		s.Telemetry.KillSwitchEnvs)
	assert.Equal(t, "v1", s.Telemetry.PromptVersion)
	assert.Equal(t, "kit-default", s.Telemetry.RedactRules)

	// And: RunStatic against the same file does not error
	// (telemetry block is additive — no existing factor breaks).
	p := writeToolspec(t, body)
	_, err := compliance.RunStatic(p)
	require.NoError(t, err)
}

func TestToolspecTelemetryBlock_AbsentLeavesNil(t *testing.T) {
	body := `name: probe
schema_version: "1"
commands:
  - name: ping
`
	s := parseSpecProbe(t, body)
	assert.Nil(t, s.Telemetry,
		"absent telemetry: block must yield nil pointer (opt-in detection)")

	// And: RunStatic still runs (backward-compat).
	p := writeToolspec(t, body)
	_, err := compliance.RunStatic(p)
	require.NoError(t, err)
}

func TestToolspecTelemetryBlock_EnabledFalse(t *testing.T) {
	body := `name: probe
schema_version: "1"
telemetry:
  enabled: false
  categories: [invocation]
  sinks: [bus]
  consent_command: "probe telemetry"
  consent_subcommands: [status, enable, disable, reset, inspect]
  kill_switch_envs: [DO_NOT_TRACK, PROBE_TELEMETRY_MODE]
  prompt_version: "v1"
  redact_rules: kit-default
`
	s := parseSpecProbe(t, body)
	require.NotNil(t, s.Telemetry,
		"block-present-but-disabled must yield non-nil pointer")
	assert.False(t, s.Telemetry.Enabled,
		"enabled:false must round-trip as false, not zero-equivalent absence")
}

// TestToolspecTelemetryBlock_PromptVersionIsString documents the
// YAML strictness choice for the load-bearing prompt_version field.
//
// Choice: gopkg.in/yaml.v3's default (non-strict) Unmarshal is
// permissive — it will coerce an unquoted integer scalar (`1`) into
// the Go string field as "1". This matches the rest of the
// toolspecYAML struct (existing fields like SchemaVersion are
// declared `string` and the codebase relies on yaml.v3's default
// permissive decode for them). We do NOT use yaml.KnownFields(true)
// or a strict decoder here.
//
// Consequence: ADR-0037 documents `prompt_version: "v1"` (quoted)
// as the canonical form. A spec author who writes `prompt_version: 1`
// will pass YAML unmarshalling but produce a `prompt_version` of
// "1", not the expected "v1". The static check (T-0699) is the
// layer that enforces the canonical value shape; the YAML layer
// only enforces parseability.
func TestToolspecTelemetryBlock_PromptVersionIsString(t *testing.T) {
	bodyQuoted := `name: probe
telemetry:
  enabled: true
  prompt_version: "v1"
`
	s := parseSpecProbe(t, bodyQuoted)
	require.NotNil(t, s.Telemetry)
	assert.Equal(t, "v1", s.Telemetry.PromptVersion)

	bodyRawInt := `name: probe
telemetry:
  enabled: true
  prompt_version: 1
`
	s2 := parseSpecProbe(t, bodyRawInt)
	require.NotNil(t, s2.Telemetry)
	assert.Equal(t, "1", s2.Telemetry.PromptVersion,
		"yaml.v3 default decode coerces int scalar to string; "+
			"strictness deferred to T-0699 static check")
}

// f13Result fetches the FactorConsentingTelemetry result from a
// report, returning nil if absent.
func f13Result(t *testing.T, body string) *compliance.CheckResult {
	t.Helper()
	p := writeToolspec(t, body)
	report, err := compliance.Run("", p)
	require.NoError(t, err)
	require.NotNil(t, report)
	for i := range report.Results {
		if report.Results[i].Factor == compliance.FactorConsentingTelemetry {
			return &report.Results[i]
		}
	}
	return nil
}

// telemetryFixtureWellFormed returns a known-good toolspec body with
// the telemetry command tree populated and all sub-conditions
// satisfied. Each TestStatic_ConsentingTelemetry_Fail* below mutates
// one field to isolate the sub-condition under test.
const telemetryFixtureWellFormed = `name: probe
schema_version: "1"
commands:
  - name: ping
  - name: telemetry
    children:
      - name: status
      - name: enable
      - name: disable
      - name: reset
      - name: inspect
telemetry:
  enabled: true
  categories: [invocation]
  sinks: [bus]
  consent_command: "probe telemetry"
  consent_subcommands: [status, enable, disable, reset, inspect]
  kill_switch_envs: [DO_NOT_TRACK, PROBE_TELEMETRY_MODE]
  prompt_version: "v1"
  redact_rules: kit-default
`

func TestStatic_ConsentingTelemetry_PassWellFormed(t *testing.T) {
	r := f13Result(t, telemetryFixtureWellFormed)
	require.NotNil(t, r)
	assert.Equal(t, "pass", r.Status, "details=%q", r.Details)
}

func TestStatic_ConsentingTelemetry_FailMissingCategory(t *testing.T) {
	body := `name: probe
schema_version: "1"
commands:
  - name: telemetry
    children:
      - name: status
      - name: enable
      - name: disable
      - name: reset
      - name: inspect
telemetry:
  enabled: true
  categories: []
  consent_command: "probe telemetry"
  consent_subcommands: [status, enable, disable, reset, inspect]
  kill_switch_envs: [DO_NOT_TRACK, PROBE_TELEMETRY_MODE]
  prompt_version: "v1"
  redact_rules: kit-default
`
	r := f13Result(t, body)
	require.NotNil(t, r)
	assert.Equal(t, "fail", r.Status)
	assert.Contains(t, r.Details, "categories")
	assert.NotEmpty(t, r.Suggestion)
}

func TestStatic_ConsentingTelemetry_FailMissingSubcommand(t *testing.T) {
	body := `name: probe
schema_version: "1"
commands:
  - name: telemetry
    children:
      - name: status
      - name: enable
      - name: disable
telemetry:
  enabled: true
  categories: [invocation]
  consent_command: "probe telemetry"
  consent_subcommands: [status, enable, disable]
  kill_switch_envs: [DO_NOT_TRACK, PROBE_TELEMETRY_MODE]
  prompt_version: "v1"
  redact_rules: kit-default
`
	r := f13Result(t, body)
	require.NotNil(t, r)
	assert.Equal(t, "fail", r.Status)
	assert.Contains(t, r.Details, "reset")
	assert.Contains(t, r.Details, "inspect")
}

func TestStatic_ConsentingTelemetry_FailMissingDoNotTrack(t *testing.T) {
	body := `name: probe
schema_version: "1"
commands:
  - name: telemetry
    children:
      - name: status
      - name: enable
      - name: disable
      - name: reset
      - name: inspect
telemetry:
  enabled: true
  categories: [invocation]
  consent_command: "probe telemetry"
  consent_subcommands: [status, enable, disable, reset, inspect]
  kill_switch_envs: [PROBE_TELEMETRY_MODE]
  prompt_version: "v1"
  redact_rules: kit-default
`
	r := f13Result(t, body)
	require.NotNil(t, r)
	assert.Equal(t, "fail", r.Status)
	assert.Contains(t, r.Details, "DO_NOT_TRACK")
}

func TestStatic_ConsentingTelemetry_FailMissingModeEnv(t *testing.T) {
	body := `name: probe
schema_version: "1"
commands:
  - name: telemetry
    children:
      - name: status
      - name: enable
      - name: disable
      - name: reset
      - name: inspect
telemetry:
  enabled: true
  categories: [invocation]
  consent_command: "probe telemetry"
  consent_subcommands: [status, enable, disable, reset, inspect]
  kill_switch_envs: [DO_NOT_TRACK]
  prompt_version: "v1"
  redact_rules: kit-default
`
	r := f13Result(t, body)
	require.NotNil(t, r)
	assert.Equal(t, "fail", r.Status)
	assert.Contains(t, r.Details, "TELEMETRY_MODE")
}

// TestStatic_ConsentingTelemetry_PassWithKitTelemetryMode covers the
// kit-binary case where the canonical `KIT_TELEMETRY_MODE` env is
// declared. The regex `^[A-Z][A-Z0-9_]*_TELEMETRY_MODE$` matches it
// by construction (no separate literal branch needed).
func TestStatic_ConsentingTelemetry_PassWithKitTelemetryMode(t *testing.T) {
	body := `name: kit
schema_version: "1"
commands:
  - name: telemetry
    children:
      - name: status
      - name: enable
      - name: disable
      - name: reset
      - name: inspect
telemetry:
  enabled: true
  categories: [invocation]
  consent_command: "kit telemetry"
  consent_subcommands: [status, enable, disable, reset, inspect]
  kill_switch_envs: [DO_NOT_TRACK, KIT_TELEMETRY_MODE]
  prompt_version: "v1"
  redact_rules: kit-default
`
	r := f13Result(t, body)
	require.NotNil(t, r)
	assert.Equal(t, "pass", r.Status, "details=%q", r.Details)
}

func TestStatic_ConsentingTelemetry_PassWithAppPrefixMode(t *testing.T) {
	body := `name: spaced
schema_version: "1"
commands:
  - name: telemetry
    children:
      - name: status
      - name: enable
      - name: disable
      - name: reset
      - name: inspect
telemetry:
  enabled: true
  categories: [invocation]
  consent_command: "spaced telemetry"
  consent_subcommands: [status, enable, disable, reset, inspect]
  kill_switch_envs: [DO_NOT_TRACK, SPACED_TELEMETRY_MODE]
  prompt_version: "v1"
  redact_rules: kit-default
`
	r := f13Result(t, body)
	require.NotNil(t, r)
	assert.Equal(t, "pass", r.Status, "details=%q", r.Details)
}

func TestStatic_ConsentingTelemetry_FailEmptyPromptVersion(t *testing.T) {
	body := `name: probe
schema_version: "1"
commands:
  - name: telemetry
    children:
      - name: status
      - name: enable
      - name: disable
      - name: reset
      - name: inspect
telemetry:
  enabled: true
  categories: [invocation]
  consent_command: "probe telemetry"
  consent_subcommands: [status, enable, disable, reset, inspect]
  kill_switch_envs: [DO_NOT_TRACK, PROBE_TELEMETRY_MODE]
  prompt_version: ""
  redact_rules: kit-default
`
	r := f13Result(t, body)
	require.NotNil(t, r)
	assert.Equal(t, "fail", r.Status)
	assert.Contains(t, r.Details, "prompt_version")
	// Field-name lock surfaces in the failure details so adopters
	// using an alias like `consent_version` know it is rejected.
	assert.Contains(t, r.Details, "consent_version",
		"prompt_version failure must name the rejected alias")
}

func TestStatic_ConsentingTelemetry_FailEmptyRedactRules(t *testing.T) {
	body := `name: probe
schema_version: "1"
commands:
  - name: telemetry
    children:
      - name: status
      - name: enable
      - name: disable
      - name: reset
      - name: inspect
telemetry:
  enabled: true
  categories: [invocation]
  consent_command: "probe telemetry"
  consent_subcommands: [status, enable, disable, reset, inspect]
  kill_switch_envs: [DO_NOT_TRACK, PROBE_TELEMETRY_MODE]
  prompt_version: "v1"
  redact_rules: ""
`
	r := f13Result(t, body)
	require.NotNil(t, r)
	assert.Equal(t, "fail", r.Status)
	assert.Contains(t, r.Details, "redact_rules")
}

// TestStatic_ConsentingTelemetry_FailMissingSubcommandInTree covers
// sub-condition (b2): consent_subcommands declared but at least one
// has no matching node in spec.Commands. Here `inspect` is declared
// in consent_subcommands but absent from the `telemetry` children.
func TestStatic_ConsentingTelemetry_FailMissingSubcommandInTree(t *testing.T) {
	body := `name: probe
schema_version: "1"
commands:
  - name: telemetry
    children:
      - name: status
      - name: enable
      - name: disable
      - name: reset
telemetry:
  enabled: true
  categories: [invocation]
  consent_command: "probe telemetry"
  consent_subcommands: [status, enable, disable, reset, inspect]
  kill_switch_envs: [DO_NOT_TRACK, PROBE_TELEMETRY_MODE]
  prompt_version: "v1"
  redact_rules: kit-default
`
	r := f13Result(t, body)
	require.NotNil(t, r)
	assert.Equal(t, "fail", r.Status)
	assert.Contains(t, r.Details, "not in commands tree")
	assert.Contains(t, r.Details, "telemetry inspect")
}

func TestStatic_ConsentingTelemetry_PassWhenSubcommandsInTree(t *testing.T) {
	r := f13Result(t, telemetryFixtureWellFormed)
	require.NotNil(t, r)
	assert.Equal(t, "pass", r.Status, "details=%q", r.Details)
	assert.Contains(t, r.Details, "well-formed")
}

// TestRun_NonOptIn_TotalIs12 verifies the score-math wiring: a
// non-opt-in spec passed through Run yields Total=12 with F13=skip.
// This is the regression check that T-0704's runtime wiring did NOT
// change non-opt-in behavior.
func TestRun_NonOptIn_TotalIs12(t *testing.T) {
	body := `name: probe
schema_version: "1"
commands:
  - name: ping
`
	p := writeToolspec(t, body)

	report, err := compliance.Run("", p)
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.Equal(t, 12, report.Total,
		"non-opt-in spec must keep Total at 12 after T-0704 runtime wiring")

	var f13 *compliance.CheckResult
	for i := range report.Results {
		if report.Results[i].Factor == compliance.FactorConsentingTelemetry {
			f13 = &report.Results[i]
			break
		}
	}
	require.NotNil(t, f13, "F13 row must be present even when skipped")
	assert.Equal(t, "skip", f13.Status,
		"non-opt-in spec must yield F13=skip")
}

// TestRun_OptIn_TotalIs13_StaticOnly verifies the score-math wiring
// for the opt-in side: a well-formed opt-in spec passed through Run
// without a binary (binaryPath="") still produces Total=13 because the
// telemetryOptedIn baseline is set at the spec level, not the binary
// level. F13 comes from the static check in this configuration.
func TestRun_OptIn_TotalIs13_StaticOnly(t *testing.T) {
	p := writeToolspec(t, telemetryFixtureWellFormed)

	report, err := compliance.Run("", p)
	require.NoError(t, err)
	require.NotNil(t, report)
	assert.Equal(t, 13, report.Total,
		"opt-in spec must bump Total to 13 (static path)")

	var f13 *compliance.CheckResult
	for i := range report.Results {
		if report.Results[i].Factor == compliance.FactorConsentingTelemetry {
			f13 = &report.Results[i]
			break
		}
	}
	require.NotNil(t, f13, "F13 row must be present")
	assert.Equal(t, "pass", f13.Status,
		"well-formed opt-in spec must yield F13=pass (static); details=%q", f13.Details)
}

func TestSpacedToolspec_HasTelemetryBlock(t *testing.T) {
	path := testdataPath()
	if _, err := os.Stat(path); err != nil {
		t.Skipf("toolspec not found: %s", path)
	}
	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	s := parseSpecProbe(t, string(raw))
	require.NotNil(t, s.Telemetry,
		"spaced toolspec must declare a telemetry: block (kept disabled)")
	assert.False(t, s.Telemetry.Enabled,
		"spaced telemetry must stay disabled until kit-telemetry + kit-consent ship")
	assert.GreaterOrEqual(t, len(s.Telemetry.Categories), 1,
		"categories list must have at least one entry")
}
