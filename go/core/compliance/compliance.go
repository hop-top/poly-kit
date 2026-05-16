// Package compliance checks CLI tools against the 12-factor AI CLI spec.
//
// It provides static checks (toolspec YAML analysis) and runtime
// checks (binary execution). Use Run for both, RunStatic/RunRuntime
// for individual passes.
package compliance

import (
	"fmt"
	"os"

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
)

var factorNames = map[Factor]string{
	FactorSelfDescribing:    "Self-Describing",
	FactorStructuredIO:      "Structured I/O",
	FactorStreamDiscipline:  "Stream Discipline",
	FactorContractsErrors:   "Contracts & Errors",
	FactorPreview:           "Preview",
	FactorIdempotency:       "Idempotency",
	FactorStateTransparency: "State Transparency",
	FactorSafeDelegation:    "Safe Delegation",
	FactorObservableOps:     "Observable Ops",
	FactorProvenance:        "Provenance",
	FactorEvolution:         "Evolution",
	FactorAuthLifecycle:     "Auth Lifecycle",
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
	return runStaticChecks(spec), nil
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

	if binaryPath != "" {
		rtResults := runRuntimeChecks(binaryPath, spec)
		results = mergeResults(results, rtResults)
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
		Total:    12,
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
	for f := FactorSelfDescribing; f <= FactorAuthLifecycle; f++ {
		if r, ok := byFactor[f]; ok {
			out = append(out, r)
		}
	}
	return out
}
