// Package citemplates_test guards the templates/ci/verify-no-leak
// adopter-facing CI workflow templates. The check is intentionally
// lightweight: each file must exist and be syntactically valid for
// its declared format. Provider-specific schema validation is out of
// scope — adopters who copy these into their CI will get full schema
// errors there.
package citemplates_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yaml "gopkg.in/yaml.v3"
)

// templatesDir returns the absolute path to templates/ci/verify-no-leak/
// by walking up from this test file's location until we find the
// templates directory. This keeps the test resilient to where `go test`
// is invoked from.
func templatesDir(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	dir := filepath.Dir(here)
	// Walk up to the repo root (containing go.mod) then descend.
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "templates", "ci", "verify-no-leak")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate repo root from %s", here)
	return ""
}

func TestTemplates_AllFilesPresent(t *testing.T) {
	dir := templatesDir(t)
	for _, name := range []string{
		"github-actions.yml",
		"gitlab-ci.yml",
		"buildkite.yml",
		"generic.sh",
		"README.md",
	} {
		_, err := os.Stat(filepath.Join(dir, name))
		assert.NoError(t, err, "expected templates/ci/verify-no-leak/%s to exist", name)
	}
}

func TestTemplates_YAMLFilesParse(t *testing.T) {
	dir := templatesDir(t)
	for _, name := range []string{
		"github-actions.yml",
		"gitlab-ci.yml",
		"buildkite.yml",
	} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, name))
			require.NoError(t, err)
			var v any
			require.NoError(t, yaml.Unmarshal(data, &v), "template %s must parse as YAML", name)
		})
	}
}

func TestTemplates_GenericShellHasShebang(t *testing.T) {
	dir := templatesDir(t)
	data, err := os.ReadFile(filepath.Join(dir, "generic.sh"))
	require.NoError(t, err)
	require.True(t, len(data) > 2, "generic.sh is empty")
	assert.True(t, strings.HasPrefix(string(data), "#!"), "generic.sh must start with a shebang")
	assert.Contains(t, string(data), "kit conformance verify-no-leak",
		"generic.sh should invoke the verify-no-leak command")
}

func TestTemplates_GitHubActions_PinsSHAs(t *testing.T) {
	dir := templatesDir(t)
	data, err := os.ReadFile(filepath.Join(dir, "github-actions.yml"))
	require.NoError(t, err)
	content := string(data)
	// Each uses: line should reference an action by 40-char SHA, not
	// by tag — see design.md §8.
	for _, line := range strings.Split(content, "\n") {
		trim := strings.TrimSpace(line)
		if !strings.HasPrefix(trim, "- uses:") && !strings.HasPrefix(trim, "uses:") {
			continue
		}
		// Skip purely commented lines (the optional --pr-body block).
		if strings.HasPrefix(trim, "#") {
			continue
		}
		// Extract the @ref portion.
		at := strings.Index(trim, "@")
		require.Greater(t, at, 0, "uses: line lacks @ref: %s", trim)
		ref := trim[at+1:]
		// Strip trailing comment if any.
		if i := strings.Index(ref, " "); i > 0 {
			ref = ref[:i]
		}
		assert.Len(t, ref, 40, "action ref must be a 40-char commit SHA, got %q on line: %s", ref, trim)
	}
}
