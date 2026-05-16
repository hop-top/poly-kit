// Black-box engine tests for Render and DryRun (spec §7-§8).
//
// Covers basic templating, path-segment vars, binary copy, exclude
// rules, conditional true/false, tier filter (with bootstrap=0
// bypass), conflict-suggested + idempotent skip, and DryRun
// no-writes semantics. Uses both fstest.MapFS (in-memory) and
// os.DirFS (real filesystem) sources to confirm engine is
// source-agnostic.
package template_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/internal/template"
)

func TestRender_BasicTemplate(t *testing.T) {
	src := fstest.MapFS{
		"main.go.tmpl": &fstest.MapFile{Data: []byte("package {{.Name}}\n")},
	}
	target := t.TempDir()
	vars := map[string]any{"Name": "foo"}

	eng := template.NewEngine(src, target, vars, template.FileRules{}, nil, 0, false)
	res, err := eng.Render(context.Background())
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(target, "main.go"))
	require.NoError(t, err)
	assert.Contains(t, string(got), "foo")
	assert.Contains(t, res.Written, filepath.Join(target, "main.go"))
}

func TestRender_VarInPath(t *testing.T) {
	// Use os.DirFS to confirm engine is source-agnostic.
	srcDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(srcDir, "src", "{{.Name}}"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(srcDir, "src", "{{.Name}}", "main.go.tmpl"),
		[]byte("package {{.Name}}\n"), 0o640,
	))

	target := t.TempDir()
	vars := map[string]any{"Name": "foo"}

	eng := template.NewEngine(os.DirFS(srcDir), target, vars, template.FileRules{}, nil, 0, false)
	_, err := eng.Render(context.Background())
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(target, "src", "foo", "main.go"))
	require.NoError(t, err)
	assert.Contains(t, string(got), "foo")
}

func TestRender_BinaryCopy(t *testing.T) {
	// Known byte sequence including non-UTF8 to prove verbatim copy.
	raw := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0xFF, 0x00}
	src := fstest.MapFS{
		"logo.png": &fstest.MapFile{Data: raw},
	}
	target := t.TempDir()
	rules := template.FileRules{Binary: []string{"*.png"}}

	eng := template.NewEngine(src, target, nil, rules, nil, 0, false)
	_, err := eng.Render(context.Background())
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(target, "logo.png"))
	require.NoError(t, err)
	assert.True(t, bytes.Equal(raw, got), "binary bytes must match verbatim")
}

func TestRender_Exclude(t *testing.T) {
	src := fstest.MapFS{
		"internal.tmp": &fstest.MapFile{Data: []byte("dropme")},
	}
	target := t.TempDir()
	rules := template.FileRules{Exclude: []string{"*.tmp"}}

	eng := template.NewEngine(src, target, nil, rules, nil, 0, false)
	res, err := eng.Render(context.Background())
	require.NoError(t, err)

	_, statErr := os.Stat(filepath.Join(target, "internal.tmp"))
	assert.True(t, os.IsNotExist(statErr), "excluded file must not exist in target")
	assert.Contains(t, res.Skipped, "internal.tmp")
}

func TestRender_ConditionalTrue(t *testing.T) {
	src := fstest.MapFS{
		"kit-conditional.AccountType=org/CODEOWNERS": &fstest.MapFile{
			Data: []byte("* @org/owners\n"),
		},
	}
	target := t.TempDir()
	vars := map[string]any{"AccountType": "org"}

	eng := template.NewEngine(src, target, vars, template.FileRules{}, nil, 0, false)
	_, err := eng.Render(context.Background())
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(target, "CODEOWNERS"))
	require.NoError(t, err)
	assert.Contains(t, string(got), "@org/owners")
}

func TestRender_ConditionalFalse(t *testing.T) {
	src := fstest.MapFS{
		"kit-conditional.AccountType=org/CODEOWNERS": &fstest.MapFile{
			Data: []byte("* @org/owners\n"),
		},
	}
	target := t.TempDir()
	vars := map[string]any{"AccountType": "personal"}

	eng := template.NewEngine(src, target, vars, template.FileRules{}, nil, 0, false)
	res, err := eng.Render(context.Background())
	require.NoError(t, err)

	_, statErr := os.Stat(filepath.Join(target, "CODEOWNERS"))
	assert.True(t, os.IsNotExist(statErr), "conditional-false file must not be written")
	// Engine SkipDirs the conditional dir on false, so Result.Conditional
	// captures the dir segment (the file under it never gets walked).
	assert.Contains(t, res.Conditional, "kit-conditional.AccountType=org")
}

func TestRender_TierFilter(t *testing.T) {
	src := fstest.MapFS{
		"foo.txt": &fstest.MapFile{Data: []byte("hello")},
	}
	target := t.TempDir()
	tiers := map[string][]int{"foo.txt": {4}}

	eng := template.NewEngine(src, target, nil, template.FileRules{}, tiers, 1, false)
	res, err := eng.Render(context.Background())
	require.NoError(t, err)

	_, statErr := os.Stat(filepath.Join(target, "foo.txt"))
	assert.True(t, os.IsNotExist(statErr), "tier-filtered file must not be written")
	assert.Contains(t, res.Skipped, "foo.txt")
}

func TestRender_BootstrapTier(t *testing.T) {
	src := fstest.MapFS{
		"foo.txt": &fstest.MapFile{Data: []byte("hello")},
		"bar.txt": &fstest.MapFile{Data: []byte("world")},
	}
	target := t.TempDir()
	tiers := map[string][]int{"foo.txt": {4}}

	// tier=0 (bootstrap): tier filter bypassed; ALL files written.
	eng := template.NewEngine(src, target, nil, template.FileRules{}, tiers, 0, false)
	_, err := eng.Render(context.Background())
	require.NoError(t, err)

	for _, name := range []string{"foo.txt", "bar.txt"} {
		_, statErr := os.Stat(filepath.Join(target, name))
		assert.NoError(t, statErr, "bootstrap mode must write %s", name)
	}
}

func TestRender_ConflictSuggested(t *testing.T) {
	src := fstest.MapFS{
		"foo.go": &fstest.MapFile{Data: []byte("new")},
	}
	target := t.TempDir()
	existing := filepath.Join(target, "foo.go")
	require.NoError(t, os.WriteFile(existing, []byte("old"), 0o640))

	eng := template.NewEngine(src, target, nil, template.FileRules{}, nil, 0, false)
	res, err := eng.Render(context.Background())
	require.NoError(t, err)

	got, err := os.ReadFile(existing)
	require.NoError(t, err)
	assert.Equal(t, "old", string(got), "original must remain unchanged")

	suggested := existing + ".kit-suggested"
	gotSug, err := os.ReadFile(suggested)
	require.NoError(t, err)
	assert.Equal(t, "new", string(gotSug), ".kit-suggested must hold new content")
	assert.Contains(t, res.Suggested, suggested)
}

func TestRender_ConflictIdempotent(t *testing.T) {
	content := []byte("identical\n")
	src := fstest.MapFS{
		"foo.go": &fstest.MapFile{Data: content},
	}
	target := t.TempDir()
	existing := filepath.Join(target, "foo.go")
	require.NoError(t, os.WriteFile(existing, content, 0o640))

	eng := template.NewEngine(src, target, nil, template.FileRules{}, nil, 0, false)
	res, err := eng.Render(context.Background())
	require.NoError(t, err)

	_, statErr := os.Stat(existing + ".kit-suggested")
	assert.True(t, os.IsNotExist(statErr), "no .kit-suggested for identical content")
	assert.NotContains(t, res.Suggested, existing+".kit-suggested")
	assert.NotContains(t, res.Written, existing, "idempotent skip must not appear in Written")
}

func TestDryRun_NoWrites(t *testing.T) {
	src := fstest.MapFS{
		"main.go.tmpl": &fstest.MapFile{Data: []byte("package {{.Name}}\n")},
	}
	target := t.TempDir()
	vars := map[string]any{"Name": "foo"}

	eng := template.NewEngine(src, target, vars, template.FileRules{}, nil, 0, false)
	res, err := eng.DryRun(context.Background())
	require.NoError(t, err)

	entries, err := os.ReadDir(target)
	require.NoError(t, err)
	assert.Empty(t, entries, "DryRun must not write anything to target")

	assert.Contains(t, res.Written, filepath.Join(target, "main.go"),
		"Result.Written must list the path that would have been written")
}

func TestRender_RenderRules_StripCustomSuffixes(t *testing.T) {
	src := fstest.MapFS{
		"main.go.t": &fstest.MapFile{Data: []byte("package {{.Name}}\n")},
		"hello.txt": &fstest.MapFile{Data: []byte("hi\n")},
	}
	target := t.TempDir()
	vars := map[string]any{"Name": "foo"}
	rules := template.RenderRules{StripSuffixes: []string{".t"}}

	eng := template.NewEngineWithRules(src, target, vars, template.FileRules{}, rules, nil, 0, false)
	_, err := eng.Render(context.Background())
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(target, "main.go"))
	assert.NoError(t, err, "stripped output must exist")
	_, err = os.Stat(filepath.Join(target, "hello.txt"))
	assert.NoError(t, err, "non-suffix file rendered as-is")
}

func TestRender_RenderRules_RemoveAfterRender(t *testing.T) {
	src := fstest.MapFS{
		"main.go.tmpl":      &fstest.MapFile{Data: []byte("package main\n")},
		"kit-template.yaml": &fstest.MapFile{Data: []byte("name: x\n")},
		"tiers.yaml":        &fstest.MapFile{Data: []byte("0: []\n")},
	}
	target := t.TempDir()
	rules := template.RenderRules{
		StripSuffixes:     []string{".tmpl"},
		RemoveAfterRender: []string{"kit-template.yaml", "tiers.yaml"},
	}

	eng := template.NewEngineWithRules(src, target, nil, template.FileRules{}, rules, nil, 0, false)
	res, err := eng.Render(context.Background())
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(target, "kit-template.yaml"))
	assert.True(t, os.IsNotExist(err), "kit-template.yaml must be removed")
	_, err = os.Stat(filepath.Join(target, "tiers.yaml"))
	assert.True(t, os.IsNotExist(err), "tiers.yaml must be removed")
	_, err = os.Stat(filepath.Join(target, "main.go"))
	assert.NoError(t, err, "main.go survives the cleanup")
	assert.Contains(t, res.Removed, "kit-template.yaml")
}

func TestRender_RenderRules_LicenseRule(t *testing.T) {
	src := fstest.MapFS{
		"main.go.tmpl":       &fstest.MapFile{Data: []byte("package main\n")},
		"LICENSE-MIT":        &fstest.MapFile{Data: []byte("MIT body\n")},
		"LICENSE-Apache-2.0": &fstest.MapFile{Data: []byte("Apache body\n")},
	}
	target := t.TempDir()
	vars := map[string]any{"License": "MIT"}
	rules := template.RenderRules{LicenseRule: &template.LicenseRule{
		Var:    "License",
		Target: "LICENSE",
		Sources: map[string]string{
			"MIT":        "LICENSE-MIT",
			"Apache-2.0": "LICENSE-Apache-2.0",
		},
	}}

	eng := template.NewEngineWithRules(src, target, vars, template.FileRules{}, rules, nil, 0, false)
	res, err := eng.Render(context.Background())
	require.NoError(t, err)

	got, err := os.ReadFile(filepath.Join(target, "LICENSE"))
	require.NoError(t, err)
	assert.Contains(t, string(got), "MIT body")
	_, err = os.Stat(filepath.Join(target, "LICENSE-MIT"))
	assert.True(t, os.IsNotExist(err), "MIT source must be cleaned up")
	_, err = os.Stat(filepath.Join(target, "LICENSE-Apache-2.0"))
	assert.True(t, os.IsNotExist(err), "Apache source must be cleaned up")
	assert.Equal(t, "LICENSE", res.LicensePicked)
}

func TestRender_RenderRules_DryRun(t *testing.T) {
	src := fstest.MapFS{
		"kit-template.yaml": &fstest.MapFile{Data: []byte("name: x\n")},
	}
	target := t.TempDir()
	rules := template.RenderRules{
		RemoveAfterRender: []string{"kit-template.yaml"},
	}

	eng := template.NewEngineWithRules(src, target, nil, template.FileRules{}, rules, nil, 0, false)
	res, err := eng.DryRun(context.Background())
	require.NoError(t, err)

	entries, err := os.ReadDir(target)
	require.NoError(t, err)
	assert.Empty(t, entries, "DryRun must not touch the filesystem")
	assert.Contains(t, res.Removed, "kit-template.yaml", "Result.Removed must reflect would-have-removed")
}
