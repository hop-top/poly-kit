// Package compliance checks CLI tools against the 12-factor AI CLI spec.
//
// It provides static checks (toolspec YAML analysis) and runtime
// checks (binary execution). Use Run for both, RunStatic/RunRuntime
// for individual passes.
package compliance

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Factor identifies one of the 12 factors.
type Factor int

const (
	FactorSelfDescribing Factor = iota + 1
	FactorStructuredIO
	FactorStreamDiscipline
	FactorContractsErrors
	FactorPreview
	FactorIdempotency
	FactorStateTransparency
	FactorSafeDelegation
	FactorObservableOps
	FactorProvenance
	FactorEvolution
	FactorAuthLifecycle
	FactorConsentingTelemetry // F13
)

var factorNames = map[Factor]string{
	FactorSelfDescribing:      "Self-Describing",
	FactorStructuredIO:        "Structured I/O",
	FactorStreamDiscipline:    "Stream Discipline",
	FactorContractsErrors:     "Contracts & Errors",
	FactorPreview:             "Preview",
	FactorIdempotency:         "Idempotency",
	FactorStateTransparency:   "State Transparency",
	FactorSafeDelegation:      "Safe Delegation",
	FactorObservableOps:       "Observable Ops",
	FactorProvenance:          "Provenance",
	FactorEvolution:           "Evolution",
	FactorAuthLifecycle:       "Auth Lifecycle",
	FactorConsentingTelemetry: "Consenting Telemetry",
}

// String returns the human-readable factor name.
func (f Factor) String() string {
	if n, ok := factorNames[f]; ok {
		return n
	}
	return fmt.Sprintf("Factor(%d)", int(f))
}

// CheckResult is the outcome of a single factor check.
type CheckResult struct {
	Factor     Factor `json:"factor"`
	Name       string `json:"name"`
	Status     string `json:"status"` // pass, fail, skip, warn
	Details    string `json:"details,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

// Report aggregates all check results for a tool.
type Report struct {
	Binary   string        `json:"binary"`
	Toolspec string        `json:"toolspec"`
	Results  []CheckResult `json:"results"`
	Score    int           `json:"score"`
	Total    int           `json:"total"`
}

// toolspecYAML mirrors the toolspec YAML schema for unmarshalling.
type toolspecYAML struct {
	Name               string          `yaml:"name"`
	SchemaVersion      string          `yaml:"schema_version"`
	Commands           []commandYAML   `yaml:"commands"`
	StateIntrospection *stateIntroYAML `yaml:"state_introspection"`
	Telemetry          *telemetryYAML  `yaml:"telemetry,omitempty"`
}

// telemetryYAML mirrors the toolspec `telemetry:` block. Subject to
// runtime + static checks under FactorConsentingTelemetry.
type telemetryYAML struct {
	Enabled            bool     `yaml:"enabled"`
	Categories         []string `yaml:"categories"`
	Sinks              []string `yaml:"sinks"`
	ConsentCommand     string   `yaml:"consent_command"`
	ConsentSubcommands []string `yaml:"consent_subcommands"`
	KillSwitchEnvs     []string `yaml:"kill_switch_envs"`
	PromptVersion      string   `yaml:"prompt_version"`
	RedactRules        string   `yaml:"redact_rules"`
}

type commandYAML struct {
	Name         string         `yaml:"name"`
	Children     []commandYAML  `yaml:"children"`
	Contract     *contractYAML  `yaml:"contract"`
	Safety       *safetyYAML    `yaml:"safety"`
	PreviewModes []string       `yaml:"preview_modes"`
	OutputSchema *outSchemaYAML `yaml:"output_schema"`
}

type contractYAML struct {
	Idempotent  *bool    `yaml:"idempotent"`
	SideEffects []string `yaml:"side_effects"`
}

type safetyYAML struct {
	Level                string `yaml:"level"`
	RequiresConfirmation bool   `yaml:"requires_confirmation"`
}

type outSchemaYAML struct {
	Format string `yaml:"format"`
}

type stateIntroYAML struct {
	ConfigCommands []string `yaml:"config_commands"`
	AuthCommands   []string `yaml:"auth_commands"`
}

// loadSpec reads and parses a toolspec YAML file.
func loadSpec(path string) (*toolspecYAML, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read toolspec: %w", err)
	}
	var spec toolspecYAML
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("parse toolspec: %w", err)
	}
	return &spec, nil
}

// RunStatic checks toolspec YAML for completeness.
func RunStatic(toolspecPath string) ([]CheckResult, error) {
	spec, err := loadSpec(toolspecPath)
	if err != nil {
		return nil, err
	}
	results := runStaticChecks(spec)
	results = append(results, checkConsentingTelemetry(spec, ""))
	return results, nil
}

// RunRuntime executes the binary and checks behavior.
func RunRuntime(binaryPath, toolspecPath string) ([]CheckResult, error) {
	spec, err := loadSpec(toolspecPath)
	if err != nil {
		return nil, err
	}
	return runRuntimeChecks(binaryPath, spec), nil
}

// Run does both static + runtime checks. If binaryPath is empty,
// only static checks run.
func Run(binaryPath, toolspecPath string) (*Report, error) {
	spec, err := loadSpec(toolspecPath)
	if err != nil {
		return nil, err
	}

	results := runStaticChecks(spec)
	results = append(results, checkConsentingTelemetry(spec, binaryPath))

	if binaryPath != "" {
		rtResults := runRuntimeChecks(binaryPath, spec)
		results = mergeResults(results, rtResults)
	}

	// Total derives from the count of factors that are *eligible* to
	// contribute (i.e. not F13-on-non-opt-in). The 12
	// pre-F13 factors are always eligible, so the denominator is 12
	// for non-opt-in binaries and 13 for opt-in binaries — preserving
	// pre-F13 backward compat (existing fixtures continue to score
	// N/12) while letting opt-in binaries score N/13. Skips inside
	// the eligible set (e.g. runtime-only factors when binaryPath is
	// empty) still count toward the denominator under this baseline
	// model; only the F13-on-non-opt-in skip is excluded.
	total := 12
	if telemetryOptedIn(spec) {
		total = 13
	}

	score := 0
	for _, r := range results {
		if r.Status == "pass" {
			score++
		}
	}

	return &Report{
		Binary:   binaryPath,
		Toolspec: toolspecPath,
		Results:  results,
		Score:    score,
		Total:    total,
	}, nil
}

// mergeResults combines static and runtime results. Runtime results
// override static "skip" entries for the same factor.
func mergeResults(static, runtime []CheckResult) []CheckResult {
	byFactor := make(map[Factor]CheckResult)
	for _, r := range static {
		byFactor[r.Factor] = r
	}
	for _, r := range runtime {
		existing, ok := byFactor[r.Factor]
		if !ok || existing.Status == "skip" {
			byFactor[r.Factor] = r
		}
	}

	out := make([]CheckResult, 0, len(byFactor))
	for f := FactorSelfDescribing; f <= FactorConsentingTelemetry; f++ {
		if r, ok := byFactor[f]; ok {
			out = append(out, r)
		}
	}
	return out
}

// telemetryOptedIn returns true iff the toolspec declares a non-nil
// telemetry block with enabled=true. Used by the FactorConsentingTelemetry
// checks to skip non-opt-in binaries.
func telemetryOptedIn(spec *toolspecYAML) bool {
	return spec != nil && spec.Telemetry != nil && spec.Telemetry.Enabled
}

// telemetryConsentSubcommandsCanonical is the canonical set of
// subcommand names that an opt-in binary MUST expose under its
// consent command (typically `<bin> telemetry`).
var telemetryConsentSubcommandsCanonical = []string{
	"status", "enable", "disable", "reset", "inspect",
}

// telemetryModeEnvShape matches `<UPPERCASE_APP>_TELEMETRY_MODE`.
// Opt-in binaries must declare DO_NOT_TRACK PLUS at least one mode env:
// either the kit-built canonical `KIT_TELEMETRY_MODE` (handled as a
// literal match) or an app-prefixed shape like `SPACED_TELEMETRY_MODE`.
var telemetryModeEnvShape = regexp.MustCompile(`^[A-Z][A-Z0-9_]*_TELEMETRY_MODE$`)

// containsString reports whether haystack contains needle.
func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// hasTelemetryModeEnv reports whether envs contains at least one
// entry matching the `<APP>_TELEMETRY_MODE` shape. The literal
// `KIT_TELEMETRY_MODE` matches the regex by construction, so no
// separate literal branch is needed.
func hasTelemetryModeEnv(envs []string) bool {
	for _, e := range envs {
		if telemetryModeEnvShape.MatchString(e) {
			return true
		}
	}
	return false
}

// commandPathExists reports whether the dotted/space-separated path
// (e.g. "telemetry status") exists in the command tree. Mirrors the
// nesting idiom used elsewhere in this package (allCommands flattens;
// path lookup walks the tree level-by-level).
func commandPathExists(cmds []commandYAML, path []string) bool {
	if len(path) == 0 {
		return false
	}
	for _, c := range cmds {
		if c.Name != path[0] {
			continue
		}
		if len(path) == 1 {
			return true
		}
		return commandPathExists(c.Children, path[1:])
	}
	return false
}

// checkConsentingTelemetry implements the F13 static check. When the
// toolspec opts into telemetry (`telemetry.enabled: true`), it asserts
// the block is well-formed across seven sub-conditions:
//
//  1. categories non-empty
//  2. consent_subcommands contains the canonical set
//     {status, enable, disable, reset, inspect}
//  3. kill_switch_envs contains DO_NOT_TRACK
//  4. kill_switch_envs contains at least one entry matching
//     `<APP>_TELEMETRY_MODE` shape (`KIT_TELEMETRY_MODE` matches by
//     construction — the regex `^[A-Z][A-Z0-9_]*_TELEMETRY_MODE$`
//     covers the kit literal AND any app-prefixed form like
//     `SPACED_TELEMETRY_MODE`)
//  5. prompt_version non-empty (field-name lock enforced
//     structurally by the YAML tag on telemetryYAML — only the
//     literal field name `prompt_version` parses; `consent_version`
//     and other aliases are silently dropped at unmarshal, which
//     surfaces here as an empty PromptVersion → fail with a
//     suggestion that names the canonical field)
//  6. redact_rules non-empty
//  7. each declared consent_subcommand maps to a real command in
//     the spec.Commands tree (e.g. consent_subcommands: [status,
//     enable] requires `<consent_command> status` and
//     `<consent_command> enable` to exist as commands)
//
// Failures are aggregated into a single CheckResult per the "single
// row per factor" model. binary is accepted for signature
// parity with the runtime checks but unused at static time.
func checkConsentingTelemetry(spec *toolspecYAML, binary string) CheckResult {
	_ = binary
	if !telemetryOptedIn(spec) {
		return skip(FactorConsentingTelemetry, "binary does not opt into telemetry")
	}

	t := spec.Telemetry
	var failures []string

	// Sub-condition (a): categories non-empty.
	if len(t.Categories) == 0 {
		failures = append(failures, "telemetry.categories is empty")
	}

	// Sub-condition (b1): canonical consent_subcommands present.
	var missingSubs []string
	for _, req := range telemetryConsentSubcommandsCanonical {
		if !containsString(t.ConsentSubcommands, req) {
			missingSubs = append(missingSubs, req)
		}
	}
	if len(missingSubs) > 0 {
		failures = append(failures, fmt.Sprintf(
			"telemetry.consent_subcommands missing required entries: %s",
			strings.Join(missingSubs, ", ")))
	}

	// Sub-conditions (c) + (d): kill-switch envs.
	if !containsString(t.KillSwitchEnvs, "DO_NOT_TRACK") {
		failures = append(failures,
			"telemetry.kill_switch_envs missing DO_NOT_TRACK")
	}
	if !hasTelemetryModeEnv(t.KillSwitchEnvs) {
		failures = append(failures,
			"telemetry.kill_switch_envs missing a <APP>_TELEMETRY_MODE "+
				"entry (e.g. KIT_TELEMETRY_MODE or SPACED_TELEMETRY_MODE)")
	}

	// Sub-condition (f, static half): prompt_version non-empty.
	// Field-name lock: the YAML tag on telemetryYAML accepts ONLY
	// `prompt_version`. Any other alias (e.g. `consent_version`)
	// surfaces here as PromptVersion=="" because the alias was
	// silently dropped during unmarshal — the suggestion below
	// names the canonical field so adopters know it is locked.
	if strings.TrimSpace(t.PromptVersion) == "" {
		failures = append(failures,
			"telemetry.prompt_version is empty (canonical field name "+
				"is `prompt_version`; aliases like `consent_version` "+
				"are not accepted)")
	}

	// Sub-condition (g, static half): redact_rules non-empty.
	if strings.TrimSpace(t.RedactRules) == "" {
		failures = append(failures, "telemetry.redact_rules is empty")
	}

	// Sub-condition (b2): every declared consent_subcommand must
	// map to a real command path in spec.Commands. The consent_command
	// schema is `<bin> telemetry [...]`; the leading `<bin>` is the
	// binary name (NOT a top-level command), so we strip it and use
	// only the tail tokens as the parent path. Fall back to
	// `telemetry <sub>` when consent_command is empty/single-token
	// (defensive — schema doesn't require consent_command, and a
	// single-token value is treated as the bare binary name).
	consentTokens := strings.Fields(t.ConsentCommand)
	var consentPath []string
	if len(consentTokens) > 1 {
		consentPath = consentTokens[1:]
	} else {
		consentPath = []string{"telemetry"}
	}
	var unmappedSubs []string
	for _, sub := range t.ConsentSubcommands {
		full := append(append([]string{}, consentPath...), sub)
		if !commandPathExists(spec.Commands, full) {
			unmappedSubs = append(unmappedSubs,
				strings.Join(full, " "))
		}
	}
	if len(unmappedSubs) > 0 {
		failures = append(failures, fmt.Sprintf(
			"telemetry.consent_subcommands declared but not in commands tree: %s",
			strings.Join(unmappedSubs, ", ")))
	}

	if len(failures) == 0 {
		return pass(FactorConsentingTelemetry,
			"telemetry block well-formed; all consent subcommands declared")
	}

	return fail(FactorConsentingTelemetry,
		strings.Join(failures, "; "),
		"Fix the telemetry block: ensure categories, "+
			"consent_subcommands {status, enable, disable, reset, "+
			"inspect}, kill_switch_envs [DO_NOT_TRACK, <APP>_TELEMETRY_MODE], "+
			"prompt_version, redact_rules are set, and that each "+
			"consent_subcommand maps to a command in the commands tree.")
}
