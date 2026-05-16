package wizard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunHeadless_AllAnswers(t *testing.T) {
	w, err := New(
		TextInput("name", "Name"),
		Select("color", "Color", []Option{
			{Value: "red", Label: "Red"},
			{Value: "blue", Label: "Blue"},
		}),
		Confirm("ok", "Proceed?"),
	)
	require.NoError(t, err)

	answers := map[string]any{
		"name":  "alice",
		"color": "red",
		"ok":    true,
	}

	results, err := RunHeadless(context.Background(), w, answers)
	require.NoError(t, err)
	assert.Equal(t, "alice", results["name"])
	assert.Equal(t, "red", results["color"])
	assert.Equal(t, true, results["ok"])
}

func TestRunHeadless_DefaultValues(t *testing.T) {
	w, err := New(
		TextInput("name", "Name").WithDefault("bob"),
		Confirm("ok", "Proceed?").WithDefault(true),
	)
	require.NoError(t, err)

	results, err := RunHeadless(context.Background(), w, map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "bob", results["name"])
	assert.Equal(t, true, results["ok"])
}

func TestRunHeadless_RequiredMissing(t *testing.T) {
	w, err := New(
		TextInput("name", "Name").WithRequired(),
	)
	require.NoError(t, err)

	_, err = RunHeadless(context.Background(), w, map[string]any{})
	require.Error(t, err)

	var ve *ValidationError
	require.True(t, errors.As(err, &ve))
	assert.Equal(t, "name", ve.StepKey)
}

func TestRunHeadless_ActionExecutes(t *testing.T) {
	var called bool
	w, err := New(
		Action("act", "Do thing", func(_ context.Context, _ map[string]any) error {
			called = true
			return nil
		}),
	)
	require.NoError(t, err)

	_, err = RunHeadless(context.Background(), w, map[string]any{})
	require.NoError(t, err)
	assert.True(t, called)
}

func TestRunHeadless_ValidationRuns(t *testing.T) {
	w, err := New(
		TextInput("name", "Name").WithRequired(),
	)
	require.NoError(t, err)

	// Provide empty string for a required field.
	_, err = RunHeadless(context.Background(), w, map[string]any{
		"name": "",
	})
	require.Error(t, err)

	var ve *ValidationError
	require.True(t, errors.As(err, &ve))
	assert.Equal(t, "name", ve.StepKey)
}

func TestRunHeadless_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	w, err := New(TextInput("name", "Name"))
	require.NoError(t, err)

	_, err = RunHeadless(ctx, w, map[string]any{"name": "x"})
	require.Error(t, err)

	var ae *AbortError
	assert.True(t, errors.As(err, &ae))
}

func TestRunHeadless_ConditionalSkip(t *testing.T) {
	w, err := New(
		Confirm("advanced", "Advanced?"),
		TextInput("extra", "Extra").WithWhen("advanced", func(v any) bool {
			b, _ := v.(bool)
			return b
		}),
	)
	require.NoError(t, err)

	// advanced=false → extra step should be skipped.
	results, err := RunHeadless(context.Background(), w, map[string]any{
		"advanced": false,
	})
	require.NoError(t, err)
	assert.Equal(t, false, results["advanced"])
	_, hasExtra := results["extra"]
	assert.False(t, hasExtra)
}

func TestLoadAnswers(t *testing.T) {
	content := "name: alice\ncount: 3\nenabled: true\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "answers.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	answers, err := LoadAnswers(path)
	require.NoError(t, err)
	assert.Equal(t, "alice", answers["name"])
	assert.Equal(t, 3, answers["count"])
	assert.Equal(t, true, answers["enabled"])
}

func TestLoadAnswers_FileNotFound(t *testing.T) {
	_, err := LoadAnswers("/nonexistent/path.yaml")
	require.Error(t, err)
}
