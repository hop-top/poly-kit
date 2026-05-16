// Engine write-path tests covering Force semantics + sacred set
// (T-0949). Force=false preserves the legacy .kit-suggested behavior
// (covered in engine_test.go); these cases exercise Force=true on
// non-sacred (overwrite) and sacred (still .kit-suggested) paths.
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

func TestRender_Force_OverwritesNonSacred(t *testing.T) {
	src := fstest.MapFS{
		"README.md": &fstest.MapFile{Data: []byte("new")},
	}
	target := t.TempDir()
	existing := filepath.Join(target, "README.md")
	require.NoError(t, os.WriteFile(existing, []byte("old"), 0o640))

	eng := template.NewEngine(src, target, nil, template.FileRules{}, nil, 0, true)
	res, err := eng.Render(context.Background())
	require.NoError(t, err)

	got, err := os.ReadFile(existing)
	require.NoError(t, err)
	assert.Equal(t, "new", string(got),
		"force=true must overwrite non-sacred existing file")

	_, statErr := os.Stat(existing + ".kit-suggested")
	assert.True(t, os.IsNotExist(statErr),
		"no .kit-suggested sibling under force=true on non-sacred path")
	assert.Contains(t, res.Written, existing)
	assert.NotContains(t, res.Suggested, existing+".kit-suggested")
}

func TestRender_Force_PreservesSacredGoMod(t *testing.T) {
	src := fstest.MapFS{
		"go.mod": &fstest.MapFile{Data: []byte("module new\n")},
	}
	target := t.TempDir()
	existing := filepath.Join(target, "go.mod")
	require.NoError(t, os.WriteFile(existing, []byte("module old\n"), 0o640))

	eng := template.NewEngine(src, target, nil, template.FileRules{}, nil, 0, true)
	res, err := eng.Render(context.Background())
	require.NoError(t, err)

	got, err := os.ReadFile(existing)
	require.NoError(t, err)
	assert.Equal(t, "module old\n", string(got),
		"force=true must NOT overwrite sacred go.mod")

	suggested := existing + ".kit-suggested"
	gotSug, err := os.ReadFile(suggested)
	require.NoError(t, err)
	assert.Equal(t, "module new\n", string(gotSug))
	assert.Contains(t, res.Suggested, suggested)
}

func TestRender_Force_PreservesSacredCmdMain(t *testing.T) {
	src := fstest.MapFS{
		"cmd/demo/main.go": &fstest.MapFile{Data: []byte("package main // new\n")},
	}
	target := t.TempDir()
	existing := filepath.Join(target, "cmd", "demo", "main.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(existing), 0o750))
	require.NoError(t, os.WriteFile(existing, []byte("package main // old\n"), 0o640))

	eng := template.NewEngine(src, target, nil, template.FileRules{}, nil, 0, true)
	res, err := eng.Render(context.Background())
	require.NoError(t, err)

	got, err := os.ReadFile(existing)
	require.NoError(t, err)
	assert.Equal(t, "package main // old\n", string(got),
		"force=true must NOT overwrite sacred cmd/*/main.go")

	suggested := existing + ".kit-suggested"
	gotSug, err := os.ReadFile(suggested)
	require.NoError(t, err)
	assert.Equal(t, "package main // new\n", string(gotSug))
	assert.Contains(t, res.Suggested, suggested)
}

func TestRender_Force_NoConflictStillWrites(t *testing.T) {
	// force=true with no pre-existing file → fresh write, no Suggested.
	src := fstest.MapFS{
		"README.md": &fstest.MapFile{Data: []byte("hello")},
	}
	target := t.TempDir()

	eng := template.NewEngine(src, target, nil, template.FileRules{}, nil, 0, true)
	res, err := eng.Render(context.Background())
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(target, "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(got))
	assert.Empty(t, res.Suggested)
}
