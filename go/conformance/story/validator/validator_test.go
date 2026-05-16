package validator_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/conformance/scenariorules"
	"hop.top/kit/go/conformance/story/parser"
	"hop.top/kit/go/conformance/story/validator"
)

func mustLoadRules(t *testing.T) *scenariorules.Document {
	t.Helper()
	doc, err := scenariorules.LoadDefault()
	require.NoError(t, err)
	return doc
}

func mustParse(t *testing.T, src string) *parser.ParsedStory {
	t.Helper()
	ps, err := parser.ParseBytes([]byte(src), "<test>")
	require.NoError(t, err)
	return ps
}

func findingRules(fs []validator.Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Rule)
	}
	return out
}

func errorFindings(fs []validator.Finding) []validator.Finding {
	var out []validator.Finding
	for _, f := range fs {
		if f.Severity == validator.SeverityError {
			out = append(out, f)
		}
	}
	return out
}

const goodStory = `schema_version: "1"
story_id: spaced.launch.dry-run
title: Preview a launch
binary: spaced
intent: An operator wants to preview a launch with dry-run before committing the operation for real.
steps:
  - id: preview
    invoke: ["spaced", "launch", "--dry-run"]
    capture: [exit_code, stdout]
`

func TestValidateGoodStoryClean(t *testing.T) {
	ps := mustParse(t, goodStory)
	fs := validator.ValidateOne(ps, validator.Options{Rules: mustLoadRules(t)})
	assert.Empty(t, errorFindings(fs), "expected no errors, got %v", fs)
}

func TestValidateMissingSchemaVersion(t *testing.T) {
	src := strings.Replace(goodStory, `schema_version: "1"`, "", 1)
	ps := mustParse(t, src)
	fs := validator.ValidateOne(ps, validator.Options{Rules: mustLoadRules(t)})
	assert.Contains(t, findingRules(fs), "missing-schema-version")
}

func TestValidateUnsupportedSchemaVersion(t *testing.T) {
	src := strings.Replace(goodStory, `schema_version: "1"`, `schema_version: "2"`, 1)
	ps := mustParse(t, src)
	fs := validator.ValidateOne(ps, validator.Options{Rules: mustLoadRules(t)})
	assert.Contains(t, findingRules(fs), "unsupported-schema-version")
}

func TestValidateMissingStoryID(t *testing.T) {
	src := strings.Replace(goodStory, "story_id: spaced.launch.dry-run\n", "", 1)
	ps := mustParse(t, src)
	fs := validator.ValidateOne(ps, validator.Options{Rules: mustLoadRules(t)})
	assert.Contains(t, findingRules(fs), "missing-story-id")
}

func TestValidateStoryIDShape(t *testing.T) {
	src := strings.Replace(goodStory, "story_id: spaced.launch.dry-run", "story_id: NotASlug", 1)
	ps := mustParse(t, src)
	fs := validator.ValidateOne(ps, validator.Options{Rules: mustLoadRules(t)})
	assert.Contains(t, findingRules(fs), "story-id-shape")
}

func TestValidateIntentLengthTooShort(t *testing.T) {
	src := strings.Replace(goodStory, "intent: An operator wants to preview a launch with dry-run before committing the operation for real.", "intent: short.", 1)
	ps := mustParse(t, src)
	fs := validator.ValidateOne(ps, validator.Options{Rules: mustLoadRules(t)})
	assert.Contains(t, findingRules(fs), "intent-length")
}

func TestValidateMissingSteps(t *testing.T) {
	src := `schema_version: "1"
story_id: a.b.c
title: t
binary: spaced
intent: A reasonably long intent that satisfies the forty character minimum requirement.
steps: []
`
	ps := mustParse(t, src)
	fs := validator.ValidateOne(ps, validator.Options{Rules: mustLoadRules(t)})
	assert.Contains(t, findingRules(fs), "missing-steps")
}

func TestValidateInvokeBinaryMismatch(t *testing.T) {
	src := strings.Replace(goodStory,
		`invoke: ["spaced", "launch", "--dry-run"]`,
		`invoke: ["bash", "-c", "echo nope"]`, 1)
	ps := mustParse(t, src)
	fs := validator.ValidateOne(ps, validator.Options{Rules: mustLoadRules(t)})
	assert.Contains(t, findingRules(fs), "invoke-binary-mismatch")
}

func TestValidateUnknownCapture(t *testing.T) {
	src := strings.Replace(goodStory, "capture: [exit_code, stdout]", "capture: [exit_code, ufo]", 1)
	ps := mustParse(t, src)
	fs := validator.ValidateOne(ps, validator.Options{Rules: mustLoadRules(t)})
	assert.Contains(t, findingRules(fs), "unknown-capture")
}

func TestValidateInvalidReferenceURL(t *testing.T) {
	src := goodStory + `references:
  - title: bad
    url: "://not a url"
`
	ps := mustParse(t, src)
	fs := validator.ValidateOne(ps, validator.Options{Rules: mustLoadRules(t)})
	assert.Contains(t, findingRules(fs), "invalid-ref-url")
}

func TestValidateForbiddenMetadataKeyScenarioID(t *testing.T) {
	src := goodStory + `metadata:
  scenario_id: oops.scenario.x
`
	ps := mustParse(t, src)
	fs := validator.ValidateOne(ps, validator.Options{Rules: mustLoadRules(t)})
	rules := findingRules(fs)
	assert.Contains(t, rules, "forbidden-metadata-key")
}

func TestValidateForbiddenMetadataKeyCassetteMustContain(t *testing.T) {
	src := goodStory + `metadata:
  cassette_must_contain: foo
`
	ps := mustParse(t, src)
	fs := validator.ValidateOne(ps, validator.Options{Rules: mustLoadRules(t)})
	rules := findingRules(fs)
	assert.Contains(t, rules, "forbidden-metadata-key")
}

func TestValidateForbiddenMetadataKeyVerb(t *testing.T) {
	src := goodStory + `metadata:
  exit_code_equals: 0
`
	ps := mustParse(t, src)
	fs := validator.ValidateOne(ps, validator.Options{Rules: mustLoadRules(t)})
	assert.Contains(t, findingRules(fs), "forbidden-metadata-key")
}

func TestValidateMetadataFreeFormKeyAllowed(t *testing.T) {
	src := goodStory + `metadata:
  authoring_notes: this is fine
`
	ps := mustParse(t, src)
	fs := validator.ValidateOne(ps, validator.Options{Rules: mustLoadRules(t)})
	assert.Empty(t, errorFindings(fs), "free-form metadata key should not be flagged; got %v", fs)
}

func TestValidateDuplicateStepID(t *testing.T) {
	src := `schema_version: "1"
story_id: a.b.c
title: t
binary: spaced
intent: A reasonably long intent that satisfies the forty character minimum requirement.
steps:
  - id: same
    invoke: ["spaced", "mission", "list"]
  - id: same
    invoke: ["spaced", "mission", "list"]
`
	ps := mustParse(t, src)
	fs := validator.ValidateOne(ps, validator.Options{Rules: mustLoadRules(t)})
	assert.Contains(t, findingRules(fs), "duplicate-step-id")
}

func TestValidateDuplicateStoryIDAcrossFiles(t *testing.T) {
	a := mustParse(t, goodStory)
	a.Path = "a.yaml"
	b := mustParse(t, goodStory)
	b.Path = "b.yaml"
	fs := validator.ValidateAll([]*parser.ParsedStory{a, b}, validator.Options{Rules: mustLoadRules(t)})
	assert.Contains(t, findingRules(fs), "duplicate-story-id")
}

func TestValidateBannedVocabularyWarning(t *testing.T) {
	src := strings.Replace(goodStory,
		"intent: An operator wants to preview a launch with dry-run before committing the operation for real.",
		"intent: The system must exit with exit code zero when preview runs and the operator should be informed.", 1)
	ps := mustParse(t, src)
	fs := validator.ValidateOne(ps, validator.Options{Rules: mustLoadRules(t)})
	var foundWarn bool
	for _, f := range fs {
		if f.Rule == "intent-banned-vocabulary" {
			assert.Equal(t, validator.SeverityWarn, f.Severity)
			foundWarn = true
		}
	}
	assert.True(t, foundWarn, "expected intent-banned-vocabulary warn; got %v", fs)
	// Critically: no error from the style warning.
	for _, f := range fs {
		if f.Rule == "intent-banned-vocabulary" {
			assert.NotEqual(t, validator.SeverityError, f.Severity)
		}
	}
}
