package wizard

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func integrationWizard() (*Wizard, error) {
	return New(
		TextInput("name", "Project name").
			WithRequired().
			WithValidateText(func(s string) error {
				if len(s) < 2 {
					return fmt.Errorf("too short")
				}
				return nil
			}),
		Select("lang", "Language", []Option{
			{Value: "go", Label: "Go"},
			{Value: "py", Label: "Python"},
			{Value: "ts", Label: "TypeScript"},
		}),
		Confirm("git", "Initialize git repo?").
			WithDefault(true),
		TextInput("extra", "Extra config").
			WithWhen("git", func(v any) bool {
				b, _ := v.(bool)
				return b
			}),
		Summary("Review"),
	)
}

func TestIntegration_Headless(t *testing.T) {
	w, err := integrationWizard()
	require.NoError(t, err)

	answers := map[string]any{
		"name":  "myapp",
		"lang":  "go",
		"git":   true,
		"extra": "some-config",
	}

	results, err := RunHeadless(context.Background(), w, answers)
	require.NoError(t, err)

	assert.Equal(t, "myapp", results["name"])
	assert.Equal(t, "go", results["lang"])
	assert.Equal(t, true, results["git"])
	assert.Equal(t, "some-config", results["extra"])
}

func TestIntegration_Line(t *testing.T) {
	w, err := integrationWizard()
	require.NoError(t, err)

	// name, select option 1 (go), confirm yes, extra value
	input := "myapp\n1\ny\nsome-config\n"
	var buf bytes.Buffer

	err = RunLine(context.Background(), w, strings.NewReader(input), &buf)
	require.NoError(t, err)

	results := w.Results()
	assert.Equal(t, "myapp", results["name"])
	assert.Equal(t, "go", results["lang"])
	assert.Equal(t, true, results["git"])
	assert.Equal(t, "some-config", results["extra"])
}

func TestIntegration_ResultsMatch(t *testing.T) {
	// Headless
	wh, err := integrationWizard()
	require.NoError(t, err)
	answers := map[string]any{
		"name": "myapp", "lang": "go", "git": true, "extra": "cfg",
	}
	headlessResults, err := RunHeadless(context.Background(), wh, answers)
	require.NoError(t, err)

	// Line
	wl, err := integrationWizard()
	require.NoError(t, err)
	input := "myapp\n1\ny\ncfg\n"
	err = RunLine(
		context.Background(), wl,
		strings.NewReader(input), io.Discard,
	)
	require.NoError(t, err)
	lineResults := wl.Results()

	// Compare (skip internal summary keys)
	for k, v := range headlessResults {
		if strings.HasPrefix(k, "__") {
			continue
		}
		assert.Equal(t, v, lineResults[k], "mismatch on key %q", k)
	}
	for k, v := range lineResults {
		if strings.HasPrefix(k, "__") {
			continue
		}
		assert.Equal(t, v, headlessResults[k], "mismatch on key %q", k)
	}
}

func TestIntegration_ConditionalSkip(t *testing.T) {
	// Headless with git=false
	wh, err := integrationWizard()
	require.NoError(t, err)
	answers := map[string]any{
		"name": "myapp", "lang": "py", "git": false,
	}
	results, err := RunHeadless(context.Background(), wh, answers)
	require.NoError(t, err)
	_, hasExtra := results["extra"]
	assert.False(t, hasExtra, "extra should be skipped when git=false")

	// Line with git=false
	wl, err := integrationWizard()
	require.NoError(t, err)
	input := "myapp\n2\nn\n" // name, python (2), no
	err = RunLine(
		context.Background(), wl,
		strings.NewReader(input), io.Discard,
	)
	require.NoError(t, err)
	lineResults := wl.Results()
	_, hasExtraLine := lineResults["extra"]
	assert.False(t, hasExtraLine,
		"extra should be skipped when git=false")
}

func TestIntegration_OnComplete(t *testing.T) {
	w, err := integrationWizard()
	require.NoError(t, err)

	var got map[string]any
	w.SetOnComplete(func(r map[string]any) error {
		got = r
		return nil
	})

	answers := map[string]any{
		"name": "test", "lang": "ts", "git": false,
	}
	_, err = RunHeadless(context.Background(), w, answers)
	require.NoError(t, err)

	assert.Equal(t, "test", got["name"])
	assert.Equal(t, "ts", got["lang"])
	assert.Equal(t, false, got["git"])
}
