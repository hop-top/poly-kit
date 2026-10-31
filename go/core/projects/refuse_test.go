package projects_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/projects"
)

// seedRaw writes raw bytes to the isolated registry path.
func seedRaw(t *testing.T, raw string) string {
	t.Helper()
	setupXDG(t)
	path, err := projects.DefaultPath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o644))
	return path
}

const (
	malformedRaw = "::: not yaml :::\n  - [unbalanced\n"
	newerRaw     = "schema: 99\nprojects:\n  keep:\n    path: /keep\n"
)

func TestWriteRefusesBadFile(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want error
	}{
		{"malformed", malformedRaw, projects.ErrMalformed},
		{"newer schema", newerRaw, projects.ErrSchemaUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := seedRaw(t, tc.raw)
			err := projects.Write("ops", projects.Entry{Path: "/x"})
			assert.ErrorIs(t, err, tc.want)
			got, rerr := os.ReadFile(path)
			require.NoError(t, rerr)
			assert.Equal(t, tc.raw, string(got), "file must be untouched")
		})
	}
}

func TestDeleteRefusesBadFile(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want error
	}{
		{"malformed", malformedRaw, projects.ErrMalformed},
		{"newer schema", newerRaw, projects.ErrSchemaUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := seedRaw(t, tc.raw)
			err := projects.Delete("keep")
			assert.ErrorIs(t, err, tc.want)
			got, rerr := os.ReadFile(path)
			require.NoError(t, rerr)
			assert.Equal(t, tc.raw, string(got), "file must be untouched")
		})
	}
}

func TestWriteDeleteMissingFileStillWorks(t *testing.T) {
	setupXDG(t)
	require.NoError(t, projects.Delete("nope"))
	require.NoError(t, projects.Write("ops", projects.Entry{Path: "/x"}))
}
