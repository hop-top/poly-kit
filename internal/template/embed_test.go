package template_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/internal/template"
)

// expectedBuiltins is the set of templates synced from templates/ via
// `make builtins-sync`. Keep in sync with internal/template/builtins/.
var expectedBuiltins = []string{"cli-go", "cli-php", "cli-py", "cli-rs", "cli-ts", "shared"}

// requireSyncedBuiltins skips when builtins/ is empty (no sync ran);
// otherwise returns the populated fs.FS.
func requireSyncedBuiltins(t *testing.T) fs.FS {
	t.Helper()
	bfs, err := template.BuiltIn()
	require.NoError(t, err)
	names, err := template.Available()
	require.NoError(t, err)
	if len(names) == 0 {
		t.Skip("internal/template/builtins/ empty; run `make builtins-sync`")
	}
	return bfs
}

func TestBuiltIn_HasCliGo(t *testing.T) {
	bfs := requireSyncedBuiltins(t)
	sub, err := fs.Sub(bfs, "cli-go")
	require.NoError(t, err)
	_, err = fs.ReadFile(sub, "kit-template.yaml")
	require.NoError(t, err, "cli-go/kit-template.yaml must be embedded")
}

func TestAvailable_ListsExpectedTemplates(t *testing.T) {
	requireSyncedBuiltins(t)
	got, err := template.Available()
	require.NoError(t, err)
	assert.Equal(t, expectedBuiltins, got, "Available() must return expected templates sorted")
}

func TestBuiltIn_EachManifestParses(t *testing.T) {
	bfs := requireSyncedBuiltins(t)
	names, err := template.Available()
	require.NoError(t, err)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			sub, err := fs.Sub(bfs, name)
			require.NoError(t, err)
			data, err := fs.ReadFile(sub, "kit-template.yaml")
			require.NoError(t, err)
			tmpf := filepath.Join(t.TempDir(), name+".yaml")
			require.NoError(t, os.WriteFile(tmpf, data, 0o644))
			_, err = template.Parse(tmpf)
			assert.NoError(t, err, "template %s manifest must parse", name)
		})
	}
}

func TestBuiltIn_EachManifestValid(t *testing.T) {
	bfs := requireSyncedBuiltins(t)
	names, err := template.Available()
	require.NoError(t, err)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			sub, err := fs.Sub(bfs, name)
			require.NoError(t, err)
			data, err := fs.ReadFile(sub, "kit-template.yaml")
			require.NoError(t, err)
			tmpf := filepath.Join(t.TempDir(), name+".yaml")
			require.NoError(t, os.WriteFile(tmpf, data, 0o644))
			m, err := template.Parse(tmpf)
			require.NoError(t, err)
			assert.NoError(t, m.Validate(), "template %s manifest must validate", name)
		})
	}
}
