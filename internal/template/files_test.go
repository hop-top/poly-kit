// Per-rule unit tests for DecideFile (spec §7).
//
// Each test exercises one branch of the precedence ladder
// (exclude → binary → conditional → .tmpl → fallback render)
// or a path-substitution edge case.
package template_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/internal/template"
)

func TestDecide_ExcludeGlob(t *testing.T) {
	rules := template.FileRules{Exclude: []string{"*.tmp"}}

	got, err := template.DecideFile("internal.tmp", nil, rules, nil)
	require.NoError(t, err)

	assert.Equal(t, template.ActionSkip, got.Action)
	assert.Empty(t, got.OutputPath)
	assert.Empty(t, got.Conditional)
}

func TestDecide_BinaryGlob(t *testing.T) {
	rules := template.FileRules{Binary: []string{"assets/*.png"}}

	got, err := template.DecideFile("assets/logo.png", nil, rules, nil)
	require.NoError(t, err)

	assert.Equal(t, template.ActionCopyVerbatim, got.Action)
	assert.Equal(t, "assets/logo.png", got.OutputPath)
	assert.Empty(t, got.Conditional)
}

func TestDecide_Tmpl(t *testing.T) {
	got, err := template.DecideFile("foo.go.tmpl", nil, template.FileRules{}, []string{".tmpl"})
	require.NoError(t, err)

	assert.Equal(t, template.ActionRender, got.Action)
	assert.Equal(t, "foo.go", got.OutputPath)
	assert.Empty(t, got.Conditional)
}

func TestDecide_NoStripWithoutSuffixes(t *testing.T) {
	// Without stripSuffixes the engine renders to source name.
	got, err := template.DecideFile("foo.go.tmpl", nil, template.FileRules{}, nil)
	require.NoError(t, err)

	assert.Equal(t, template.ActionRender, got.Action)
	assert.Equal(t, "foo.go.tmpl", got.OutputPath)
}

func TestDecide_VarInPathSegment(t *testing.T) {
	vars := map[string]any{"Name": "myapp"}

	got, err := template.DecideFile("src/{{.Name}}/main.go", vars, template.FileRules{}, nil)
	require.NoError(t, err)

	assert.Equal(t, template.ActionRender, got.Action)
	assert.Equal(t, "src/myapp/main.go", got.OutputPath)
	assert.Empty(t, got.Conditional)
}

func TestDecide_Conditional_True(t *testing.T) {
	vars := map[string]any{"AccountType": "org"}

	got, err := template.DecideFile(
		"kit-conditional.AccountType=org/CODEOWNERS",
		vars,
		template.FileRules{},
		nil,
	)
	require.NoError(t, err)

	assert.Equal(t, template.ActionConditional, got.Action)
	assert.Equal(t, "CODEOWNERS", got.OutputPath)
	assert.Equal(t, "AccountType=org", got.Conditional)
}

func TestDecide_Conditional_False(t *testing.T) {
	// DecideFile is a classifier — it captures the expression
	// regardless of vars. The engine evaluates the expression and
	// decides whether to render or skip the subtree.
	vars := map[string]any{"AccountType": "personal"}

	got, err := template.DecideFile(
		"kit-conditional.AccountType=org/CODEOWNERS",
		vars,
		template.FileRules{},
		nil,
	)
	require.NoError(t, err)

	assert.Equal(t, template.ActionConditional, got.Action)
	assert.Equal(t, "CODEOWNERS", got.OutputPath)
	assert.Equal(t, "AccountType=org", got.Conditional)
}

func TestDecide_BinaryAndTmpl(t *testing.T) {
	// Binary rule must win over .tmpl suffix. Using "assets/*.tmpl" so
	// the glob actually matches the path under path.Match semantics
	// ("*.png" alone would not match "foo.png.tmpl" — the literal
	// ".png" at end does not align). The point is precedence: when
	// binary matches, .tmpl suffix is NOT stripped (verbatim copy).
	rules := template.FileRules{Binary: []string{"assets/*.tmpl"}}

	got, err := template.DecideFile("assets/foo.png.tmpl", nil, rules, nil)
	require.NoError(t, err)

	assert.Equal(t, template.ActionCopyVerbatim, got.Action)
	assert.Equal(t, "assets/foo.png.tmpl", got.OutputPath)
	assert.Empty(t, got.Conditional)
}

func TestDecide_VarSubstitutionError(t *testing.T) {
	// missingkey=error → executing {{.Missing}} against an empty map
	// must surface as an error from DecideFile.
	_, err := template.DecideFile("{{.Missing}}/main.go", map[string]any{}, template.FileRules{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Missing")
}
