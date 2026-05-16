package scenario

import "time"

// Scenario is the top-level closed-key shape of a scenario YAML.
// yaml.v3 with KnownFields(true) enforces the closed key set; every
// adopter-authored top-level key not declared on this struct fails
// the parse with SCENARIO_PARSE_ERROR.
//
// The shape matches contracts/scenario-rules.json's top_level_keys
// list; drift is flagged by the leak-rule consistency test (see
// verbs/registry_test.go).
type Scenario struct {
	ScenarioID             string         `yaml:"scenario_id" json:"scenario_id"`
	SchemaVersion          string         `yaml:"schema_version" json:"schema_version"`
	Binary                 string         `yaml:"binary" json:"binary"`
	FactorCoverage         []int          `yaml:"factor_coverage" json:"factor_coverage"`
	Tier                   int            `yaml:"tier" json:"tier"`
	StoryRef               StoryRef       `yaml:"story_ref" json:"story_ref"`
	Steps                  []Step         `yaml:"steps" json:"steps"`
	Assertions             []Assertion    `yaml:"assertions" json:"assertions"`
	Description            string         `yaml:"description,omitempty" json:"description,omitempty"`
	EngineMinGraderVersion string         `yaml:"engine_min_grader_version,omitempty" json:"engine_min_grader_version,omitempty"`
	Judges                 []JudgeBlock   `yaml:"judge,omitempty" json:"judge,omitempty"`
	Preconditions          []Precondition `yaml:"preconditions,omitempty" json:"preconditions,omitempty"`
	Actors                 []Actor        `yaml:"actors,omitempty" json:"actors,omitempty"`
	Grading                *Grading       `yaml:"grading,omitempty" json:"grading,omitempty"`
	Metadata               map[string]any `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

// StoryRef binds a scenario to its underlying user story by
// content-addressed hash. The grader refuses to grade if the
// supplied story bytes do not hash to ContentHash.
type StoryRef struct {
	StoryID            string `yaml:"story_id" json:"story_id"`
	StoryPath          string `yaml:"story_path" json:"story_path"`
	ContentHash        string `yaml:"content_hash" json:"content_hash"`
	StorySchemaVersion string `yaml:"story_schema_version,omitempty" json:"story_schema_version,omitempty"`
}

// Step is one invocation in the scenario's command sequence. The
// grader assumes a capture was recorded for every step before
// grading begins; capture[] is documentation, not enforcement.
type Step struct {
	ID      string            `yaml:"id" json:"id"`
	Invoke  []string          `yaml:"invoke" json:"invoke"`
	Actor   string            `yaml:"actor,omitempty" json:"actor,omitempty"`
	Capture []string          `yaml:"capture,omitempty" json:"capture,omitempty"`
	Env     map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	Stdin   string            `yaml:"stdin,omitempty" json:"stdin,omitempty"`
	Delay   string            `yaml:"delay,omitempty" json:"delay,omitempty"`
}

// Assertion is one verb invocation in the scenario rubric. Per-kind
// arguments are carried inline via the embedded map; the validator
// pre-checks shape against the closed table in design §2.
//
// The grader walks Assertions in declaration order; the verbs/
// registry resolves Kind to its evaluator.
type Assertion struct {
	ID     string `yaml:"id" json:"id"`
	Kind   string `yaml:"kind" json:"kind"`
	On     string `yaml:"on,omitempty" json:"on,omitempty"`
	Factor int    `yaml:"factor" json:"factor"`

	// Args carries per-kind arguments. YAML decodes inline keys
	// (everything beyond id/kind/on/factor) here so authors write
	// flat YAML. JSON marshaling nests under "args" for stable
	// wire-format identity.
	Args map[string]any `yaml:",inline" json:"args,omitempty"`
}

// JudgeBlock declares an AI judge the grader can consult via the
// AIJudge interface (judge/judge.go). Multiple judge blocks per
// scenario are allowed; each is keyed by ID and referenced by one or
// more judge_score_above assertions.
type JudgeBlock struct {
	ID             string        `yaml:"id" json:"id"`
	On             string        `yaml:"on" json:"on"`
	Prompt         string        `yaml:"prompt,omitempty" json:"prompt,omitempty"`
	PromptRef      string        `yaml:"prompt_ref,omitempty" json:"prompt_ref,omitempty"`
	Model          string        `yaml:"model" json:"model"`
	ModelAllowlist []string      `yaml:"model_allowlist" json:"model_allowlist"`
	RequiredScore  float64       `yaml:"required_score,omitempty" json:"required_score,omitempty"`
	Timeout        time.Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	MaxTokens      int           `yaml:"max_tokens,omitempty" json:"max_tokens,omitempty"`
}

// Precondition is reserved in v1: parsed and validated for closed
// keys, but not evaluated by the grader. Reserved for the next
// schema version (multi-actor scenarios).
type Precondition struct {
	ID          string         `yaml:"id" json:"id"`
	Description string         `yaml:"description,omitempty" json:"description,omitempty"`
	Metadata    map[string]any `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

// Actor is reserved in v1: parsed and validated for closed keys, but
// not consulted by the grader. Step.Actor references resolve here
// once multi-actor evaluation lands.
type Actor struct {
	ID          string `yaml:"id" json:"id"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// Grading lets a scenario override the default verdict-aggregation
// policy. pass_if=all_assertions_pass is the v1 default.
type Grading struct {
	PassIf string `yaml:"pass_if,omitempty" json:"pass_if,omitempty"`
}

// PassIfAll is the default Grading.PassIf value.
const (
	PassIfAll = "all_assertions_pass"
	PassIfAny = "any_assertion_passes"
)
