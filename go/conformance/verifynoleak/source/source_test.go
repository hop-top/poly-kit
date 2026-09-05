package source_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/conformance/verifynoleak/scanner"
	"hop.top/kit/go/conformance/verifynoleak/source"
)

// mkTree creates each relative path under root as an empty file,
// making parent directories as needed.
func mkTree(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		full := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte("name: x\n"), 0o644))
	}
}

// TestPaths_DirectoryExpansion pins the --paths semantics: bare files
// pass through verbatim (missing or unsupported included), directory
// entries recurse collecting only scanner-supported files, dot-dirs
// are pruned, and the result is deduplicated.
func TestPaths_DirectoryExpansion(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files []string // tree to create (relative to tmp root)
		dirs  []string // extra empty dirs to create
		paths []string // --paths input (relative to tmp root)
		want  []string // expected output (relative to tmp root)
		// lenient drives PathsAllowingMissing instead of Paths. Set on
		// the cases that assert the opt-in pass-through behavior:
		// under the strict default these same inputs are ErrBadPaths.
		lenient bool
	}{
		{
			name:  "dir with supported files recursed",
			files: []string{"d/a.yaml", "d/b.md", "d/c.go"},
			paths: []string{"d"},
			want:  []string{"d/a.yaml", "d/b.md"},
		},
		{
			name:  "nested dirs recursed",
			files: []string{"d/sub/deep/x.yml", "d/top.markdown"},
			paths: []string{"d"},
			want:  []string{"d/sub/deep/x.yml", "d/top.markdown"},
		},
		{
			name:  "dot-dir pruned",
			files: []string{"d/.hidden/leak.yaml", "d/ok.yaml"},
			paths: []string{"d"},
			want:  []string{"d/ok.yaml"},
		},
		{
			name:  "explicit dot-dir root still walked",
			files: []string{".hidden/leak.yaml"},
			paths: []string{".hidden"},
			want:  []string{".hidden/leak.yaml"},
		},
		{
			name:  "unsupported files in dir not collected",
			files: []string{"d/a.yaml", "d/b.txt", "d/c.go", "d/noext"},
			paths: []string{"d"},
			want:  []string{"d/a.yaml"},
		},
		{
			name:  "explicit unsupported file passes through",
			files: []string{"main.go"},
			paths: []string{"main.go"},
			want:  []string{"main.go"},
		},
		{
			name:    "missing entry passes through under --allow-missing-paths",
			files:   nil,
			paths:   []string{"nope.yaml"},
			want:    []string{"nope.yaml"},
			lenient: true,
		},
		{
			name:  "dedup file passed directly and via its dir",
			files: []string{"d/a.yaml", "d/b.yaml"},
			paths: []string{"d", "d/a.yaml"},
			want:  []string{"d/a.yaml", "d/b.yaml"},
		},
		{
			name:  "walk output lexicographic within a dir",
			files: []string{"d/z.yaml", "d/a.yaml", "d/m.md"},
			paths: []string{"d"},
			want:  []string{"d/a.yaml", "d/m.md", "d/z.yaml"},
		},
		{
			name:    "empty dir yields nothing under --allow-missing-paths",
			files:   []string{"other/x.yaml"},
			dirs:    []string{"empty"},
			paths:   []string{"empty", "other"},
			want:    []string{"other/x.yaml"},
			lenient: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			mkTree(t, root, tc.files...)
			for _, d := range tc.dirs {
				require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o755))
			}
			resolve := source.Paths
			if tc.lenient {
				resolve = source.PathsAllowingMissing
			}
			got, err := resolve(root, tc.paths, scanner.SupportedPath)
			require.NoError(t, err)
			want := make([]string, 0, len(tc.want))
			for _, w := range tc.want {
				want = append(want, filepath.Join(root, w))
			}
			assert.Equal(t, want, got)
		})
	}
}

func TestPaths_NilPredicateCollectsEverything(t *testing.T) {
	root := t.TempDir()
	mkTree(t, root, "d/a.yaml", "d/b.go")
	got, err := source.Paths(root, []string{"d"}, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{
		filepath.Join(root, "d/a.yaml"),
		filepath.Join(root, "d/b.go"),
	}, got)
}

func TestPaths_EmptyInputErrors(t *testing.T) {
	_, err := source.Paths(t.TempDir(), nil, scanner.SupportedPath)
	require.Error(t, err)
}
