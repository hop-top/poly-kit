package ps_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/ps"
)

type mockProvider struct {
	entries []ps.Entry
	err     error
}

func (m *mockProvider) List(_ context.Context) ([]ps.Entry, error) {
	return m.entries, m.err
}

func TestCommand_Default_FiltersDone(t *testing.T) {
	p := &mockProvider{entries: testEntries()}
	v := viper.New()
	cmd := ps.Command("test", p, v)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "abc-001")
	assert.Contains(t, out, "abc-002")
	assert.NotContains(t, out, "abc-003") // done, filtered
}

func TestCommand_All_IncludesDone(t *testing.T) {
	p := &mockProvider{entries: testEntries()}
	v := viper.New()
	cmd := ps.Command("test", p, v)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--all"})
	err := cmd.Execute()
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "abc-003") // done, included
}

func TestCommand_JSON(t *testing.T) {
	p := &mockProvider{entries: testEntries()[:1]}
	v := viper.New()
	cmd := ps.Command("test", p, v)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--json"})
	err := cmd.Execute()
	require.NoError(t, err)

	assert.Contains(t, buf.String(), `"id"`)
	assert.Contains(t, buf.String(), `"abc-001"`)
}

func TestCommand_Quiet(t *testing.T) {
	p := &mockProvider{entries: testEntries()[:1]}
	v := viper.New()
	v.Set("quiet", true)
	cmd := ps.Command("test", p, v)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.NoError(t, err)

	assert.Equal(t, "abc-001\n", buf.String())
}

func TestCommand_ProviderError(t *testing.T) {
	p := &mockProvider{err: assert.AnError}
	v := viper.New()
	cmd := ps.Command("test", p, v)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.Error(t, err)
}

func TestFilterActive(t *testing.T) {
	entries := []ps.Entry{
		{ID: "a", Status: ps.StatusRunning},
		{ID: "b", Status: ps.StatusDone},
		{ID: "c", Status: ps.StatusPending},
	}

	// Use command with default (no --all) to test filtering
	p := &mockProvider{entries: entries}
	v := viper.New()
	v.Set("quiet", true)
	cmd := ps.Command("test", p, v)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "a")
	assert.NotContains(t, out, "b")
	assert.Contains(t, out, "c")
}
