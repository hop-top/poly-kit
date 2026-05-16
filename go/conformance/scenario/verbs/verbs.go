// Package verbs implements the closed-enum verb roster the scenario
// grader dispatches on. Each verb has a canonical Kind* string, a
// per-verb argument validator (ValidateArgs), and an evaluator
// implementing the Evaluator interface.
//
// The registry below is the single source of truth for which verbs
// the grader understands. The leak-rule consistency test in
// registry_test.go cross-checks this set against
// contracts/scenario-rules.json — drift between the two surfaces
// causes CI to fail rather than allowing a verb in the wire format
// that the grader cannot evaluate (or vice-versa).
package verbs

// Verb kind constants. These are the wire-format strings authors
// type in scenario YAML under assertions[].kind. The list is closed
// in v1; new verbs require a rules_version bump in
// contracts/scenario-rules.json plus an evaluator registration here.
const (
	KindExitCodeEquals            = "exit_code_equals"
	KindExitCodeIn                = "exit_code_in"
	KindExitCodeClass             = "exit_code_class"
	KindOutputFieldEquals         = "output_field_equals"
	KindOutputFieldPresent        = "output_field_present"
	KindOutputFieldCount          = "output_field_count"
	KindOutputSchemaMatches       = "output_schema_matches"
	KindCassetteMustContain       = "cassette_must_contain"
	KindCassetteMustNotContain    = "cassette_must_not_contain"
	KindCassetteDiffEquals        = "cassette_diff_equals"
	KindCassetteDiffEmpty         = "cassette_diff_empty"
	KindDestructiveGateRequired   = "destructive_gate_required"
	KindDryRunNoMutation          = "dry_run_no_mutation"
	KindIdempotencyReplayClean    = "idempotency_replay_clean"
	KindCapabilityRoundtrip       = "capability_roundtrip"
	KindJudgeScoreAbove           = "judge_score_above"
	KindStderrContains            = "stderr_contains"
	KindStderrDoesNotContain      = "stderr_does_not_contain"
	KindStreamDisciplinePass      = "stream_discipline_pass"
	KindProvenancePresent         = "provenance_present"
	KindProvenanceMatchesCassette = "provenance_matches_cassette"
	KindAuthLifecycleClean        = "auth_lifecycle_clean"
)

// AllKinds returns every verb kind the grader recognizes. The order
// is stable (declaration order in the registry) so tests and docs
// can rely on it.
func AllKinds() []string {
	out := make([]string, 0, len(registry))
	out = append(out, registryOrder...)
	return out
}

// IsKnown reports whether k is a registered verb kind.
func IsKnown(k string) bool {
	_, ok := registry[k]
	return ok
}

// Entry holds the per-verb argument validator and (when present) the
// evaluator. auth_lifecycle_clean has Validate but no Evaluate — the
// grader emits not_implemented for that kind per Q2.
type Entry struct {
	Kind     string
	Validate func(args map[string]any) []string
	// Evaluate may be nil; nil means "parsed but not implemented".
	// The grader maps nil-Evaluate to AssertionResult{Status:
	// StatusNotImplemented}.
	Evaluate Evaluator
}

// registry maps Kind to its Entry. Populated by per-verb init()
// functions in sibling files (exit_code.go, output_field.go, etc.).
// Direct mutation outside init() is unsafe.
var (
	registry      = map[string]*Entry{}
	registryOrder []string
)

// register is the per-verb init helper. Verb files call this from
// init() to wire themselves in. The first call wins; double
// registration panics (programmer error).
func register(e *Entry) {
	if e == nil || e.Kind == "" {
		panic("verbs.register: missing Kind")
	}
	if _, ok := registry[e.Kind]; ok {
		panic("verbs.register: duplicate kind " + e.Kind)
	}
	registry[e.Kind] = e
	registryOrder = append(registryOrder, e.Kind)
}

// ValidateArgs returns the per-verb argument validation errors for
// kind given args. An unknown kind returns a single error. A known
// kind with no Validate function returns nil (no per-kind shape).
func ValidateArgs(kind string, args map[string]any) []string {
	e, ok := registry[kind]
	if !ok {
		return []string{"unknown verb " + kind}
	}
	if e.Validate == nil {
		return nil
	}
	return e.Validate(args)
}

// Lookup returns the registered entry for kind, or nil.
func Lookup(kind string) *Entry {
	if e, ok := registry[kind]; ok {
		return e
	}
	return nil
}
