package verifynoleak_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/conformance/verifynoleak/scanner"
)

// ── T-1226 negative cases ────────────────────────────────────────
//
// "Shape mimics but is not a scenario" — these legitimate file
// shapes must NOT trip the detector even though they share
// vocabulary with scenarios.

func TestNegative_SpacedToolspecShape(t *testing.T) {
	// Mirrors examples/spaced/spaced.toolspec.yaml. Top-level keys
	// are name/schema_version/commands — different shape from a
	// scenario which has scenario_id/assertions/judge at root.
	dir := t.TempDir()
	p := writeFile(t, dir, "spaced.toolspec.yaml", `name: spaced
schema_version: 1
commands:
  - name: launch
    intent: send vehicle to orbit
    contract:
      input_schema: {}
      output_schema: {}
    side_effects: [network, fs]
    safety:
      destructive: true
`)
	results, err := scanner.Scan([]string{p}, scanner.Options{Rules: loadRules(t)})
	require.NoError(t, err)
	assert.Empty(t, allFindings(results), "kit's own toolspec example shape must not trip the detector")
}

func TestNegative_PolicyConfigShape(t *testing.T) {
	// kit/scope policy uses rules: with effect/path entries.
	dir := t.TempDir()
	p := writeFile(t, dir, "policy.yaml", `rules:
  - effect: allow
    path: /tmp/**
  - effect: deny
    path: /etc/shadow
defaults:
  effect: deny
`)
	results, err := scanner.Scan([]string{p}, scanner.Options{Rules: loadRules(t)})
	require.NoError(t, err)
	assert.Empty(t, allFindings(results))
}

func TestNegative_TemplateManifestShape(t *testing.T) {
	// Resembles templates/*/tiers.yaml or templates/*/kit-template.yaml.
	dir := t.TempDir()
	p := writeFile(t, dir, "kit-template.yaml", `tiers:
  free:
    limits:
      requests_per_day: 100
  paid:
    limits:
      requests_per_day: 10000
features:
  - name: dashboards
    enabled_in: [paid]
`)
	results, err := scanner.Scan([]string{p}, scanner.Options{Rules: loadRules(t)})
	require.NoError(t, err)
	assert.Empty(t, allFindings(results))
}

func TestNegative_PlainChangelogMarkdown(t *testing.T) {
	// A CHANGELOG.md with no fenced YAML is structurally inert.
	dir := t.TempDir()
	p := writeFile(t, dir, "CHANGELOG.md", `# Changelog

## v1.2.0 — 2026-04-01

- Added the launch command.
- Fixed the cassette_must_not_contain check (mentioned in prose only).
- Refactored the scenario_id resolver internally.

## v1.1.0 — 2026-03-15

- Initial release.
`)
	results, err := scanner.Scan([]string{p}, scanner.Options{Rules: loadRules(t)})
	require.NoError(t, err)
	assert.Empty(t, allFindings(results), "prose-only mentions of scenario terms must not trip")
}

func TestNegative_OpenAPISpecShape(t *testing.T) {
	// OpenAPI specs use keywords like "exit_code" and "judge" sometimes
	// — but their top-level shape is openapi/info/paths.
	dir := t.TempDir()
	p := writeFile(t, dir, "openapi.yaml", `openapi: 3.0.0
info:
  title: example api
  version: 1
paths:
  /launch:
    post:
      summary: launch the thing
      responses:
        "200":
          description: OK
`)
	results, err := scanner.Scan([]string{p}, scanner.Options{Rules: loadRules(t)})
	require.NoError(t, err)
	assert.Empty(t, allFindings(results))
}

func TestNegative_NonScenarioAssertionsListShape(t *testing.T) {
	// terraform-like, OPA-rego, or cucumber-style configs may use
	// assertions: as a list, but their kinds are not in our verb
	// set. R2 requires >=2 known verbs.
	dir := t.TempDir()
	p := writeFile(t, dir, "assertions.yaml", `assertions:
  - kind: terraform_plan_change_is_zero
  - kind: opa_rule_passes
  - kind: cucumber_step_passes
`)
	results, err := scanner.Scan([]string{p}, scanner.Options{Rules: loadRules(t)})
	require.NoError(t, err)
	assert.Empty(t, allFindings(results), "unknown verbs in assertions list must not fire R2")
}

func TestNegative_BareJudgeKeyword(t *testing.T) {
	// design.md §1: bare "judge:" is too common; R4 requires the
	// combination of prompt(_ref) and required_score/model.
	dir := t.TempDir()
	p := writeFile(t, dir, "doc.yaml", `judge: this is just a string
metadata:
  judge: see the appellate court ruling
`)
	results, err := scanner.Scan([]string{p}, scanner.Options{Rules: loadRules(t)})
	require.NoError(t, err)
	assert.Empty(t, allFindings(results))
}

func TestNegative_ScenarioWordInProseOnly(t *testing.T) {
	// A doc that mentions "scenario" repeatedly in prose, with no
	// structural shape, must not trip. survey §3 explicitly rejects
	// the prose-token approach as having extreme false-positive
	// rate.
	dir := t.TempDir()
	p := writeFile(t, dir, "guide.md", `# Testing scenarios

We write scenarios in the rubric format. Each scenario has
assertions and may use a judge model. The scenario_id is the
canonical reference, and the cassette_must_not_contain rules let
you assert on output.

(All of these are mentioned in prose only — no fenced YAML.)
`)
	results, err := scanner.Scan([]string{p}, scanner.Options{Rules: loadRules(t)})
	require.NoError(t, err)
	assert.Empty(t, allFindings(results), "prose mentions of scenario tokens are out of scope")
}
