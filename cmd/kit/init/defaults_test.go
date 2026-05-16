package kitinit_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	kitinit "hop.top/kit/cmd/kit/init"
)

func TestPath_RespectsXDG(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)

	path, err := kitinit.Path()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "kit", "defaults.yaml"), path)
}

func TestRead_Missing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	d, err := kitinit.Read()
	require.NoError(t, err)
	assert.Equal(t, kitinit.Defaults{}, d)
}

func TestWrite_Read_Roundtrip(t *testing.T) {
	tBool := true
	fBool := false

	cases := []struct {
		name string
		in   kitinit.Defaults
	}{
		{
			name: "hop_nil",
			in: kitinit.Defaults{
				Author:           "Alice",
				Email:            "alice@example.com",
				AccountType:      "personal",
				Org:              "",
				Visibility:       "public",
				License:          "MIT",
				Theme:            "dark",
				Template:         "go-cli",
				TemplateRegistry: "hop.top/registry",
				Runtime:          []string{"go", "node"},
				Hop:              nil,
			},
		},
		{
			name: "hop_true",
			in: kitinit.Defaults{
				Author:      "Bob",
				Email:       "bob@example.com",
				AccountType: "org",
				Org:         "acme",
				Visibility:  "private",
				License:     "Apache-2.0",
				Runtime:     []string{"python"},
				Hop:         &tBool,
			},
		},
		{
			name: "hop_false",
			in: kitinit.Defaults{
				Author:      "Carol",
				Email:       "carol@example.com",
				AccountType: "none",
				Visibility:  "internal",
				License:     "BSD-3-Clause",
				Hop:         &fBool,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())

			require.NoError(t, kitinit.Write(tc.in))
			got, err := kitinit.Read()
			require.NoError(t, err)

			assert.Equal(t, tc.in.Author, got.Author)
			assert.Equal(t, tc.in.Email, got.Email)
			assert.Equal(t, tc.in.AccountType, got.AccountType)
			assert.Equal(t, tc.in.Org, got.Org)
			assert.Equal(t, tc.in.Visibility, got.Visibility)
			assert.Equal(t, tc.in.License, got.License)
			assert.Equal(t, tc.in.Theme, got.Theme)
			assert.Equal(t, tc.in.Template, got.Template)
			assert.Equal(t, tc.in.TemplateRegistry, got.TemplateRegistry)
			// YAML round-trip may turn nil slices into empty slices; compare
			// by length + contents rather than insisting on nil identity.
			assert.Len(t, got.Runtime, len(tc.in.Runtime))
			for i := range tc.in.Runtime {
				assert.Equal(t, tc.in.Runtime[i], got.Runtime[i])
			}

			if tc.in.Hop == nil {
				assert.Nil(t, got.Hop, "Hop must round-trip as nil when source was nil")
			} else {
				require.NotNil(t, got.Hop)
				assert.Equal(t, *tc.in.Hop, *got.Hop)
			}
		})
	}
}

func TestRead_Malformed(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	path, err := kitinit.Path()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte("::: not yaml :::\n\t- {{{"), 0o644))

	d, err := kitinit.Read()
	require.NoError(t, err, "malformed YAML must not propagate error")
	assert.Equal(t, kitinit.Defaults{}, d)
}

func TestWrite_PreservesPermissions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	require.NoError(t, kitinit.Write(kitinit.Defaults{Author: "Dee"}))

	path, err := kitinit.Path()
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}
