package wizard

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_ForceLine(t *testing.T) {
	w, err := New(
		TextInput("name", "Name").WithDefault("alice"),
	)
	require.NoError(t, err)

	var out bytes.Buffer
	in := strings.NewReader("\n") // accept default

	err = Run(context.Background(), w, ForceLine(),
		WithInput(in), WithOutput(&out))
	require.NoError(t, err)
	assert.Equal(t, "alice", w.Results()["name"])
}

func TestRun_ForceTUI_WithoutProvider(t *testing.T) {
	w, err := New(TextInput("name", "Name"))
	require.NoError(t, err)

	err = Run(context.Background(), w, ForceTUI())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TUI frontend not provided")
}

func TestRun_ForceTUI_WithProvider(t *testing.T) {
	w, err := New(TextInput("name", "Name"))
	require.NoError(t, err)

	var called bool
	mock := func(_ context.Context, _ *Wizard) error {
		called = true
		return nil
	}

	err = Run(context.Background(), w, ForceTUI(), WithTUI(mock))
	require.NoError(t, err)
	assert.True(t, called)
}

func TestRun_WithAnswers(t *testing.T) {
	w, err := New(
		TextInput("name", "Name"),
		Confirm("ok", "Proceed?"),
	)
	require.NoError(t, err)

	err = Run(context.Background(), w, WithAnswers(map[string]any{
		"name": "bob",
		"ok":   true,
	}))
	require.NoError(t, err)
	assert.Equal(t, "bob", w.Results()["name"])
	assert.Equal(t, true, w.Results()["ok"])
}

func TestRun_OnComplete(t *testing.T) {
	w, err := New(TextInput("name", "Name").WithDefault("x"))
	require.NoError(t, err)

	var received map[string]any
	err = Run(context.Background(), w,
		WithAnswers(map[string]any{"name": "x"}),
		OnComplete(func(r map[string]any) error {
			received = r
			return nil
		}),
	)
	require.NoError(t, err)
	assert.Equal(t, "x", received["name"])
}

func TestRun_OnComplete_Error(t *testing.T) {
	w, err := New(TextInput("name", "Name").WithDefault("x"))
	require.NoError(t, err)

	err = Run(context.Background(), w,
		WithAnswers(map[string]any{"name": "x"}),
		OnComplete(func(_ map[string]any) error {
			return errors.New("boom")
		}),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func TestRun_Line_CallsComplete(t *testing.T) {
	w, err := New(
		TextInput("name", "Name").WithDefault("alice"),
	)
	require.NoError(t, err)

	var completeCalled bool
	var out bytes.Buffer
	in := strings.NewReader("\n") // accept default

	err = Run(context.Background(), w, ForceLine(),
		WithInput(in), WithOutput(&out),
		OnComplete(func(r map[string]any) error {
			completeCalled = true
			return nil
		}),
	)
	require.NoError(t, err)
	assert.True(t, completeCalled,
		"OnComplete must fire via Run() with ForceLine")
}

func TestRun_WithDryRun(t *testing.T) {
	w, err := New(TextInput("name", "Name").WithDefault("x"))
	require.NoError(t, err)

	var completeCalled bool
	err = Run(context.Background(), w,
		WithAnswers(map[string]any{"name": "x"}),
		WithDryRun(),
		OnComplete(func(_ map[string]any) error {
			completeCalled = true
			return nil
		}),
	)
	require.NoError(t, err)
	assert.False(t, completeCalled, "OnComplete should not run in dry-run mode")
}
