package ps_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/ps"
)

func testEntries() []ps.Entry {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return []ps.Entry{
		{
			ID:       "abc-001",
			Status:   ps.StatusRunning,
			Worker:   "agent-1",
			Scope:    "build/deploy",
			Started:  base,
			Progress: &ps.Progress{Done: 3, Total: 10},
		},
		{
			ID:       "abc-002",
			Status:   ps.StatusPending,
			Worker:   "agent-2",
			Worktree: "feat/login",
			Track:    "auth-flow",
			Scope:    "test/integration",
			Started:  base,
		},
		{
			ID:      "abc-003",
			Status:  ps.StatusDone,
			Worker:  "agent-3",
			Scope:   "cleanup",
			Started: base,
		},
	}
}

func TestRender_Table_ContainsHeaders(t *testing.T) {
	var buf bytes.Buffer
	now := time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)
	err := ps.RenderAt(&buf, testEntries(), "table", true, now)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "ID")
	assert.Contains(t, out, "STATUS")
	assert.Contains(t, out, "WORKER")
	assert.Contains(t, out, "SCOPE")
	assert.Contains(t, out, "DURATION")
	assert.Contains(t, out, "PROGRESS")
}

func TestRender_Table_OptionalColumns(t *testing.T) {
	var buf bytes.Buffer
	now := time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)
	err := ps.RenderAt(&buf, testEntries(), "table", true, now)
	require.NoError(t, err)

	out := buf.String()
	// abc-002 has Worktree and Track, so columns should appear.
	assert.Contains(t, out, "WORKTREE")
	assert.Contains(t, out, "TRACK")
	assert.Contains(t, out, "feat/login")
	assert.Contains(t, out, "auth-flow")
}

func TestRender_Table_NoOptionalColumns(t *testing.T) {
	entries := []ps.Entry{{
		ID:      "x-1",
		Status:  ps.StatusRunning,
		Worker:  "w1",
		Scope:   "test",
		Started: time.Now(),
	}}
	var buf bytes.Buffer
	err := ps.RenderAt(&buf, entries, "table", true, time.Now())
	require.NoError(t, err)

	out := buf.String()
	assert.NotContains(t, out, "WORKTREE")
	assert.NotContains(t, out, "TRACK")
}

func TestRender_Table_Empty(t *testing.T) {
	var buf bytes.Buffer
	err := ps.Render(&buf, nil, "table", true)
	require.NoError(t, err)
	assert.Empty(t, buf.String())
}

func TestRender_Table_Duration(t *testing.T) {
	var buf bytes.Buffer
	now := time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC)
	err := ps.RenderAt(&buf, testEntries(), "table", true, now)
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "5m")
}

func TestRender_JSON(t *testing.T) {
	var buf bytes.Buffer
	err := ps.Render(&buf, testEntries(), "json", false)
	require.NoError(t, err)

	var decoded []ps.Entry
	err = json.Unmarshal(buf.Bytes(), &decoded)
	require.NoError(t, err)
	require.Len(t, decoded, 3)
	assert.Equal(t, "abc-001", decoded[0].ID)
	assert.Equal(t, ps.StatusRunning, decoded[0].Status)
	assert.Equal(t, 3, decoded[0].Progress.Done)
}

func TestRender_Quiet(t *testing.T) {
	var buf bytes.Buffer
	err := ps.Render(&buf, testEntries(), "quiet", false)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, "abc-001", lines[0])
	assert.Equal(t, "abc-002", lines[1])
	assert.Equal(t, "abc-003", lines[2])
}
