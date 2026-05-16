package template_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/internal/template"
)

const sampleManifest = `name: cli-go
description: A Go CLI starter template
kit_version: ">= 0.1.0"
variables:
  - name: app_name
    prompt: Application name?
    required: true
    default: myapp
    type: string
  - name: license
    prompt: License?
    type: choice
    choices: [MIT, Apache-2.0]
files:
  exclude:
    - "**/*.tmp"
    - "secrets.txt"
  binary:
    - "**/*.png"
hooks:
  pre_render:
    - hooks/pre.sh
  post_render:
    - hooks/post.sh
`

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "kit-template.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestParse_Roundtrip(t *testing.T) {
	path := writeTemp(t, sampleManifest)

	m, err := template.Parse(path)
	require.NoError(t, err)

	assert.Equal(t, "cli-go", m.Name)
	assert.Equal(t, "A Go CLI starter template", m.Description)
	assert.Equal(t, ">= 0.1.0", m.KitVersion)

	require.Len(t, m.Variables, 2)
	assert.Equal(t, "app_name", m.Variables[0].Name)
	assert.Equal(t, "Application name?", m.Variables[0].Prompt)
	assert.True(t, m.Variables[0].Required)
	assert.Equal(t, "myapp", m.Variables[0].Default)
	assert.Equal(t, "string", m.Variables[0].Type)

	assert.Equal(t, "license", m.Variables[1].Name)
	assert.Equal(t, "choice", m.Variables[1].Type)
	assert.Equal(t, []string{"MIT", "Apache-2.0"}, m.Variables[1].Choices)

	assert.Equal(t, []string{"**/*.tmp", "secrets.txt"}, m.Files.Exclude)
	assert.Equal(t, []string{"**/*.png"}, m.Files.Binary)

	assert.Equal(t, []string{"hooks/pre.sh"}, m.Hooks.PreRender)
	assert.Equal(t, []string{"hooks/post.sh"}, m.Hooks.PostRender)
}

func TestParse_FileMissing(t *testing.T) {
	_, err := template.Parse("/nonexistent/path/kit-template.yaml")
	require.Error(t, err)
	assert.True(t, errors.Is(err, template.ErrManifestInvalid),
		"expected ErrManifestInvalid, got %v", err)
}

func TestParse_MalformedYAML(t *testing.T) {
	// Unterminated flow mapping + bad indentation guarantees a YAML
	// parse error rather than coercion to a scalar string.
	path := writeTemp(t, "name: {unterminated\n  - bad: [\n")

	_, err := template.Parse(path)
	require.Error(t, err)
	assert.True(t, errors.Is(err, template.ErrManifestInvalid),
		"expected ErrManifestInvalid, got %v", err)
}

func TestValidate_NameRequired(t *testing.T) {
	m := template.Manifest{Name: ""}
	err := m.Validate()
	require.Error(t, err)
	assert.True(t, errors.Is(err, template.ErrManifestInvalid))
}

func TestValidate_BadKitVersion(t *testing.T) {
	m := template.Manifest{Name: "x", KitVersion: "not-a-semver"}
	err := m.Validate()
	require.Error(t, err)
	assert.True(t, errors.Is(err, template.ErrManifestInvalid))
}

func TestValidate_ChoiceWithoutChoices(t *testing.T) {
	m := template.Manifest{
		Name: "x",
		Variables: []template.Variable{
			{Name: "v", Type: "choice", Choices: nil},
		},
	}
	err := m.Validate()
	require.Error(t, err)
	assert.True(t, errors.Is(err, template.ErrManifestInvalid))
}

func TestValidate_BadValidateRegex(t *testing.T) {
	m := template.Manifest{
		Name: "x",
		Variables: []template.Variable{
			{Name: "v", Validate: "["},
		},
	}
	err := m.Validate()
	require.Error(t, err)
	assert.True(t, errors.Is(err, template.ErrManifestInvalid))
}

func TestValidate_AbsoluteHookPath(t *testing.T) {
	m := template.Manifest{
		Name:  "x",
		Hooks: template.Hooks{PreRender: []string{"/etc/foo"}},
	}
	err := m.Validate()
	require.Error(t, err)
	assert.True(t, errors.Is(err, template.ErrManifestInvalid))
}

func TestValidate_DotDotHookPath(t *testing.T) {
	m := template.Manifest{
		Name:  "x",
		Hooks: template.Hooks{PreRender: []string{"../foo"}},
	}
	err := m.Validate()
	require.Error(t, err)
	assert.True(t, errors.Is(err, template.ErrManifestInvalid))
}

const renderRulesManifest = `name: cli-go
render_rules:
  strip_suffixes: [".tmpl"]
  remove_after_render:
    - "kit-template.yaml"
    - "tiers.yaml"
  license:
    var: License
    target: LICENSE
    sources:
      MIT: LICENSE-MIT
      Apache-2.0: LICENSE-Apache-2.0
`

func TestParse_RenderRules(t *testing.T) {
	path := writeTemp(t, renderRulesManifest)

	m, err := template.Parse(path)
	require.NoError(t, err)

	assert.Equal(t, []string{".tmpl"}, m.RenderRules.StripSuffixes)
	assert.Equal(t, []string{"kit-template.yaml", "tiers.yaml"}, m.RenderRules.RemoveAfterRender)
	require.NotNil(t, m.RenderRules.LicenseRule)
	assert.Equal(t, "License", m.RenderRules.LicenseRule.Var)
	assert.Equal(t, "LICENSE", m.RenderRules.LicenseRule.Target)
	assert.Equal(t, "LICENSE-MIT", m.RenderRules.LicenseRule.Sources["MIT"])

	require.NoError(t, m.Validate())
}

func TestValidate_StripSuffixWithoutDot(t *testing.T) {
	m := template.Manifest{
		Name:        "x",
		RenderRules: template.RenderRules{StripSuffixes: []string{"tmpl"}},
	}
	err := m.Validate()
	require.Error(t, err)
	assert.True(t, errors.Is(err, template.ErrManifestInvalid))
}

func TestValidate_AbsoluteRemovePath(t *testing.T) {
	m := template.Manifest{
		Name:        "x",
		RenderRules: template.RenderRules{RemoveAfterRender: []string{"/etc/foo"}},
	}
	err := m.Validate()
	require.Error(t, err)
	assert.True(t, errors.Is(err, template.ErrManifestInvalid))
}

func TestValidate_DotDotRemovePath(t *testing.T) {
	m := template.Manifest{
		Name:        "x",
		RenderRules: template.RenderRules{RemoveAfterRender: []string{"../etc"}},
	}
	err := m.Validate()
	require.Error(t, err)
	assert.True(t, errors.Is(err, template.ErrManifestInvalid))
}

func TestValidate_LicenseRuleMissingVar(t *testing.T) {
	m := template.Manifest{
		Name: "x",
		RenderRules: template.RenderRules{LicenseRule: &template.LicenseRule{
			Target:  "LICENSE",
			Sources: map[string]string{"MIT": "LICENSE-MIT"},
		}},
	}
	err := m.Validate()
	require.Error(t, err)
	assert.True(t, errors.Is(err, template.ErrManifestInvalid))
}

func TestValidate_LicenseRuleEmptySources(t *testing.T) {
	m := template.Manifest{
		Name: "x",
		RenderRules: template.RenderRules{LicenseRule: &template.LicenseRule{
			Var:     "License",
			Target:  "LICENSE",
			Sources: map[string]string{},
		}},
	}
	err := m.Validate()
	require.Error(t, err)
	assert.True(t, errors.Is(err, template.ErrManifestInvalid))
}
