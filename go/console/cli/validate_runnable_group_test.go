package cli

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runnableGroupRoot returns a root with every leaf annotated and four
// groups: "item" runs on its own and declares nothing, "odd" runs on
// its own with a value that names no tier, "fine" runs on its own
// with a tier, and "pure" has no action of its own.
func runnableGroupRoot(t *testing.T, cfg Config) (*Root, *bytes.Buffer) {
	t.Helper()
	cfg.Name, cfg.Version, cfg.DisableValidate = "fix", "1.0.0", true
	r := New(cfg)
	leafAnn := map[string]string{"kit/side-effect": "read", "kit/idempotent": "yes"}
	group := func(name string, ann map[string]string, runnable bool) {
		g := &cobra.Command{Use: name, Short: name, Annotations: ann}
		if runnable {
			g.Run = func(*cobra.Command, []string) {}
		}
		g.AddCommand(&cobra.Command{Use: "list", Short: "list", Annotations: leafAnn,
			Run: func(*cobra.Command, []string) {}})
		r.Cmd.AddCommand(g)
	}
	group("item", map[string]string{"kit/idempotent": "yes"}, true)
	group("odd", map[string]string{"kit/side-effect": "wrte", "kit/idempotent": "yes"}, true)
	group("fine", map[string]string{"kit/side-effect": "read", "kit/idempotent": "yes"}, true)
	group("pure", nil, false)
	var errBuf bytes.Buffer
	r.Cmd.SetErr(&errBuf)
	return r, &errBuf
}

// TestValidateWarnsOnUnclassifiedRunnableGroup: a group with its own
// Run is invoked like a leaf and spec coverage counts it, so Validate
// names it when it declares no tier; it warns rather than refuses, so
// a tool that booted before keeps booting.
func TestValidateWarnsOnUnclassifiedRunnableGroup(t *testing.T) {
	r, errBuf := runnableGroupRoot(t, Config{})

	require.NoError(t, r.Validate(), "a runnable group is warned about, never refused")
	out := errBuf.String()
	assert.Contains(t, out, "\n  fix item\n")
	assert.Contains(t, out, "\n  fix odd=\"wrte\"\n")
	assert.NotContains(t, out, "fix fine")
	assert.NotContains(t, out, "fix pure")

	require.NoError(t, r.Validate())
	assert.Equal(t, out, errBuf.String(), "the warning is written once per root")
}

// TestValidateRunnableGroupWarningQuiet: QuietBootWarnings silences it.
func TestValidateRunnableGroupWarningQuiet(t *testing.T) {
	r, errBuf := runnableGroupRoot(t, Config{QuietBootWarnings: true})

	require.NoError(t, r.Validate())
	assert.Empty(t, errBuf.String())
}
