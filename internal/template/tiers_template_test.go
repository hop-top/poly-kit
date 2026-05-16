// T-0950: tiers.yaml keys may contain text/template variables that
// reference the same vars used for file rendering. The engine must
// render keys before matching against post-substitution output paths,
// or var-bearing keys silently fall through to the default tier [4].
package template_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/internal/template"
)

func TestTierKey_RendersTemplateBeforeMatch(t *testing.T) {
	src := fstest.MapFS{
		"cmd/{{.name}}/main.go": &fstest.MapFile{
			Data: []byte("package main\n"),
		},
	}
	target := t.TempDir()
	vars := map[string]any{"name": "demo"}
	// Var-bearing key — must be rendered before tier match.
	tiers := map[string][]int{"cmd/{{.name}}/main.go": {3}}

	eng := template.NewEngine(src, target, vars, template.FileRules{}, tiers, 3, false)
	res, err := eng.Render(context.Background())
	require.NoError(t, err)

	out := filepath.Join(target, "cmd", "demo", "main.go")
	_, statErr := os.Stat(out)
	assert.NoError(t, statErr, "tier=3 file with var-bearing key must be written")
	assert.Contains(t, res.Written, out)
	assert.NotContains(t, res.Skipped, "cmd/{{.name}}/main.go",
		"file must not be skipped due to literal-key mismatch")
}

func TestTierKey_LiteralKeyStillWorks(t *testing.T) {
	src := fstest.MapFS{
		"main.go": &fstest.MapFile{Data: []byte("package main\n")},
	}
	target := t.TempDir()
	tiers := map[string][]int{"main.go": {3}}

	eng := template.NewEngine(src, target, nil, template.FileRules{}, tiers, 3, false)
	_, err := eng.Render(context.Background())
	require.NoError(t, err)

	out := filepath.Join(target, "main.go")
	_, statErr := os.Stat(out)
	assert.NoError(t, statErr, "literal key tier=3 must still match")
}

func TestTierKey_MalformedTemplate_Errors(t *testing.T) {
	src := fstest.MapFS{
		"foo.txt": &fstest.MapFile{Data: []byte("hi")},
	}
	target := t.TempDir()
	// Unclosed action — text/template parse fails.
	tiers := map[string][]int{"cmd/{{.bad/x.go": {1}}

	eng := template.NewEngine(src, target, map[string]any{}, template.FileRules{}, tiers, 1, false)
	_, err := eng.Render(context.Background())
	require.Error(t, err, "malformed tier key must surface a non-nil error")
	assert.Contains(t, err.Error(), "cmd/{{.bad/x.go",
		"error must mention the offending key")
}
