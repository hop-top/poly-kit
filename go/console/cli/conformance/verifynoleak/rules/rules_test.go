package rules_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"hop.top/kit/go/console/cli/conformance/verifynoleak/rules"
)

func mustLoad(t *testing.T) *rules.Set {
	t.Helper()
	set, err := rules.LoadDefault()
	require.NoError(t, err)
	require.NotNil(t, set)
	return set
}

func parse(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(src), &n))
	return &n
}

func findingIDs(fs []rules.Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.RuleID
	}
	return out
}

// ── R1: scenario_id at root ────────────────────────────────────────

func TestApply_R1_FiresOnTopLevelScenarioID(t *testing.T) {
	set := mustLoad(t)
	doc := parse(t, `scenario_id: spaced.launch.dry-run-clean
unrelated: ok
`)
	fs := rules.Apply(set, doc)
	assert.Contains(t, findingIDs(fs), "R1")
}

func TestApply_R1_DoesNotFireOnNested(t *testing.T) {
	set := mustLoad(t)
	doc := parse(t, `metadata:
  scenario_id: nested-not-root
`)
	fs := rules.Apply(set, doc)
	assert.NotContains(t, findingIDs(fs), "R1", "R1 must require scenario_id at the document root")
}

// ── R2: assertions list with >=2 known verbs ───────────────────────

func TestApply_R2_FiresOnTwoKnownVerbs(t *testing.T) {
	set := mustLoad(t)
	doc := parse(t, `assertions:
  - kind: exit_code_equals
    value: 0
  - kind: cassette_must_not_contain
    pattern: PASSWORD=
`)
	fs := rules.Apply(set, doc)
	assert.Contains(t, findingIDs(fs), "R2")
}

func TestApply_R2_DoesNotFireOnOneKnownVerb(t *testing.T) {
	set := mustLoad(t)
	doc := parse(t, `assertions:
  - kind: exit_code_equals
    value: 0
  - kind: random_unrelated_check
    value: 7
`)
	fs := rules.Apply(set, doc)
	// R2 wants >= 2; one known verb should not fire R2.
	// But R3 might fire if cassette_must_* appears — keep the example clean.
	for _, f := range fs {
		assert.NotEqual(t, "R2", f.RuleID, "R2 must require >= 2 known verbs")
	}
}

func TestApply_R2_IgnoresUnknownVerbs(t *testing.T) {
	set := mustLoad(t)
	doc := parse(t, `assertions:
  - kind: not_a_real_verb
  - kind: another_fake_verb
  - kind: third_fake_verb
`)
	fs := rules.Apply(set, doc)
	for _, f := range fs {
		assert.NotEqual(t, "R2", f.RuleID)
	}
}

// ── R3: cassette_must_(not_)contain anywhere ──────────────────────

func TestApply_R3_FiresOnNestedCassetteMustContain(t *testing.T) {
	set := mustLoad(t)
	doc := parse(t, `steps:
  - run: foo
    expect:
      cassette_must_contain: hello
`)
	fs := rules.Apply(set, doc)
	assert.Contains(t, findingIDs(fs), "R3")
}

func TestApply_R3_FiresOnTopLevelCassetteMustNotContain(t *testing.T) {
	set := mustLoad(t)
	doc := parse(t, `cassette_must_not_contain: SECRET
`)
	fs := rules.Apply(set, doc)
	assert.Contains(t, findingIDs(fs), "R3")
}

// ── R4: judge block with prompt + score/model ─────────────────────

func TestApply_R4_FiresOnPromptAndScore(t *testing.T) {
	set := mustLoad(t)
	doc := parse(t, `judge:
  prompt: "rate the answer 0-10"
  required_score: 7
`)
	fs := rules.Apply(set, doc)
	assert.Contains(t, findingIDs(fs), "R4")
}

func TestApply_R4_FiresOnPromptRefAndModel(t *testing.T) {
	set := mustLoad(t)
	doc := parse(t, `judge:
  prompt_ref: rubric/v1.md
  model: claude-opus
`)
	fs := rules.Apply(set, doc)
	assert.Contains(t, findingIDs(fs), "R4")
}

func TestApply_R4_DoesNotFireOnBareJudge(t *testing.T) {
	set := mustLoad(t)
	doc := parse(t, `judge: this is just a comment
other: stuff
`)
	fs := rules.Apply(set, doc)
	for _, f := range fs {
		assert.NotEqual(t, "R4", f.RuleID, "bare judge: scalar must not trip R4")
	}
}

func TestApply_R4_DoesNotFireOnPromptOnly(t *testing.T) {
	set := mustLoad(t)
	doc := parse(t, `judge:
  prompt: "rate the answer 0-10"
`)
	fs := rules.Apply(set, doc)
	for _, f := range fs {
		assert.NotEqual(t, "R4", f.RuleID, "R4 requires both prompt and score/model")
	}
}

// ── Negative cases — survey §4 hot zones ──────────────────────────

func TestApply_NoFindings_OnSpacedToolspec(t *testing.T) {
	// Shape mimics examples/spaced/spaced.toolspec.yaml top level.
	// Should NOT trip any rule.
	set := mustLoad(t)
	doc := parse(t, `name: spaced
schema_version: 1
commands:
  - name: launch
    intent: send vehicle to orbit
    contract:
      input_schema: {}
      output_schema: {}
    side_effects: [network, fs]
`)
	fs := rules.Apply(set, doc)
	assert.Empty(t, fs, "kit's own toolspec example must not trip the leak detector")
}

func TestApply_NoFindings_OnPolicyConfig(t *testing.T) {
	set := mustLoad(t)
	doc := parse(t, `rules:
  - effect: allow
    path: /tmp/**
  - effect: deny
    path: /etc/**
`)
	fs := rules.Apply(set, doc)
	assert.Empty(t, fs)
}

func TestApply_NoFindings_OnEmptyDocument(t *testing.T) {
	set := mustLoad(t)
	doc := parse(t, ``)
	assert.Empty(t, rules.Apply(set, doc))
}

func TestApply_NoFindings_OnScalarRoot(t *testing.T) {
	set := mustLoad(t)
	doc := parse(t, `just a string`)
	assert.Empty(t, rules.Apply(set, doc))
}

func TestApply_NoFindings_OnSequenceRoot(t *testing.T) {
	set := mustLoad(t)
	doc := parse(t, `- one
- two
- three
`)
	assert.Empty(t, rules.Apply(set, doc))
}

// ── Loader: schema validation ─────────────────────────────────────

func TestLoadDefault_ProvidesAllFourRules(t *testing.T) {
	set := mustLoad(t)
	assert.Equal(t, "1", set.SchemaVersion)
	assert.NotEmpty(t, set.RulesVersion)
	assert.NotEmpty(t, set.Verbs)
	assert.Len(t, set.Rules, 4)
	gotIDs := make([]string, len(set.Rules))
	for i, r := range set.Rules {
		gotIDs[i] = r.ID
	}
	assert.ElementsMatch(t, []string{"R1", "R2", "R3", "R4"}, gotIDs)
}

func TestLoadFromPath_RejectsUnknownSchemaVersion(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "rules.json")
	require.NoError(t, os.WriteFile(tmp, []byte(`{"schema_version":"99","rules_version":"x","compound_rules":[]}`), 0o644))
	_, err := rules.LoadFromPath(tmp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "schema_version")
}

func TestLoadFromPath_RejectsUnknownRuleKind(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "rules.json")
	body := `{
	  "schema_version":"1",
	  "rules_version":"x",
	  "verbs":[],
	  "top_level_keys":[],
	  "compound_rules":[{"id":"RX","description":"future","kind":"semantic_paraphrase"}]
	}`
	require.NoError(t, os.WriteFile(tmp, []byte(body), 0o644))
	_, err := rules.LoadFromPath(tmp)
	require.Error(t, err)
	assert.ErrorIs(t, err, rules.ErrUnknownRuleKind, "unknown kinds must fail loud, not silently skip")
}

func TestLoadFromPath_RejectsKeyAtRootWithoutKey(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "rules.json")
	body := `{
	  "schema_version":"1",
	  "rules_version":"x",
	  "compound_rules":[{"id":"R1","kind":"key_at_root"}]
	}`
	require.NoError(t, os.WriteFile(tmp, []byte(body), 0o644))
	_, err := rules.LoadFromPath(tmp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key")
}

func TestLoadFromPath_FileNotFound(t *testing.T) {
	_, err := rules.LoadFromPath(filepath.Join(t.TempDir(), "does-not-exist.json"))
	require.Error(t, err)
}

// ── Finding shape ─────────────────────────────────────────────────

func TestFinding_CarriesLineNumber(t *testing.T) {
	set := mustLoad(t)
	src := `# leading comment
some_other_key: x
scenario_id: spaced.launch.dry-run-clean
`
	doc := parse(t, src)
	fs := rules.Apply(set, doc)
	var r1 *rules.Finding
	for i := range fs {
		if fs[i].RuleID == "R1" {
			r1 = &fs[i]
			break
		}
	}
	require.NotNil(t, r1)
	assert.Equal(t, 3, r1.Line, "scenario_id is on line 3 of the source")
	assert.Equal(t, []string{"scenario_id"}, r1.MatchedKeys)
}

// ── Identity guard — multiple rules can fire on one doc ───────────

func TestApply_FullScenarioFiresMultipleRules(t *testing.T) {
	// A maximally leak-shaped doc should trip R1, R2, R3, and R4 all
	// at once — the "well-meaning author pastes a scenario into the
	// README" scenario from the survey. Note: R3 looks for
	// cassette_must_(not_)contain as a key, distinct from R2's
	// kind-value match.
	set := mustLoad(t)
	doc := parse(t, `scenario_id: spaced.launch.full-rubric
assertions:
  - kind: exit_code_equals
    value: 0
  - kind: stderr_contains
    value: launched
steps:
  - run: launch
    expect:
      cassette_must_not_contain: SECRET
judge:
  prompt: rate clarity 0-10
  required_score: 7
`)
	fs := rules.Apply(set, doc)
	ids := findingIDs(fs)
	assert.Contains(t, ids, "R1")
	assert.Contains(t, ids, "R2")
	assert.Contains(t, ids, "R3")
	assert.Contains(t, ids, "R4")
}

func TestErrUnknownRuleKind_IsSentinel(t *testing.T) {
	// Guard the public sentinel so downstream packages can use
	// errors.Is for switch-style dispatch.
	require.True(t, errors.Is(rules.ErrUnknownRuleKind, rules.ErrUnknownRuleKind))
}
