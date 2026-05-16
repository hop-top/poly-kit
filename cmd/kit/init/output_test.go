package kitinit_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	kitinit "hop.top/kit/cmd/kit/init"
	"hop.top/kit/internal/template"
)

func sampleSummary() kitinit.Summary {
	return kitinit.Summary{
		Mode:     "bootstrap",
		Name:     "myapp",
		Target:   "/tmp/myapp",
		Template: "go-cli",
		Result: template.Result{
			Written:     []string{"go.mod", "main.go", "README.md"},
			Suggested:   []string{".gitignore.kit-suggested"},
			Skipped:     []string{"docs/legacy.md"},
			Conditional: []string{"docker/Dockerfile"},
		},
		GitHub: &kitinit.GitHubSummary{
			Repo:       "acme/myapp",
			URL:        "https://github.com/acme/myapp",
			Visibility: "private",
		},
		NextSteps: []string{"cd myapp", "make build", "./bin/myapp --help"},
	}
}

func TestWriteHuman_BasicShape(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, kitinit.WriteHuman(&buf, sampleSummary()))

	out := buf.String()
	assert.Contains(t, out, "myapp")
	assert.Contains(t, out, "/tmp/myapp")
	assert.Contains(t, out, "go-cli")
	assert.Contains(t, out, "Files written:")
	assert.Contains(t, out, "main.go")
	assert.Contains(t, out, "Suggested files:")
	assert.Contains(t, out, ".gitignore.kit-suggested")
	assert.Contains(t, out, "Skipped: 1")
	assert.Contains(t, out, "Conditional: 1")
	assert.Contains(t, out, "GitHub:")
	assert.Contains(t, out, "https://github.com/acme/myapp")
	assert.Contains(t, out, "Next steps:")
	assert.Contains(t, out, "1. cd myapp")
}

func TestWriteHuman_TruncatesFiles(t *testing.T) {
	s := sampleSummary()
	s.Result.Written = make([]string, 25)
	for i := range s.Result.Written {
		s.Result.Written[i] = fmt.Sprintf("file-%02d.go", i)
	}

	var buf bytes.Buffer
	require.NoError(t, kitinit.WriteHuman(&buf, s))
	out := buf.String()

	// First 10 entries shown.
	for i := 0; i < 10; i++ {
		assert.Contains(t, out, fmt.Sprintf("file-%02d.go", i))
	}
	// 11th entry not shown.
	assert.NotContains(t, out, "file-10.go")
	// Trailer reports remaining count.
	assert.Contains(t, out, "... (15 more)")
}

func TestWriteJSON_RoundTrip(t *testing.T) {
	src := sampleSummary()

	var buf bytes.Buffer
	require.NoError(t, kitinit.WriteJSON(&buf, src))

	var got kitinit.Summary
	require.NoError(t, json.NewDecoder(&buf).Decode(&got))

	assert.Equal(t, src, got)
}

func TestNextSteps_Bootstrap(t *testing.T) {
	got := kitinit.NextSteps("bootstrap", "myapp", nil)
	assert.Equal(t, []string{
		"cd myapp",
		"make build",
		"./bin/myapp --help",
	}, got)
}

func TestNextSteps_Augment(t *testing.T) {
	got := kitinit.NextSteps("augment", "myapp", nil)
	assert.Equal(t, []string{
		"review .kit-suggested.* files",
		"make build",
		"make test",
	}, got)
}

func TestNextSteps_UnknownMode(t *testing.T) {
	assert.Nil(t, kitinit.NextSteps("other", "myapp", nil))
}

func TestWriteHuman_NoGitHub(t *testing.T) {
	s := sampleSummary()
	s.GitHub = nil

	var buf bytes.Buffer
	require.NoError(t, kitinit.WriteHuman(&buf, s))

	out := buf.String()
	assert.NotContains(t, strings.ToLower(out), "github")
	assert.NotContains(t, out, "https://")
}
