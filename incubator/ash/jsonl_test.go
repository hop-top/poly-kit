package ash_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hop.top/ash"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newJSONLStore(t *testing.T) (*ash.JSONLStore, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sessions")
	return ash.NewJSONLStore(dir), dir
}

func TestJSONLStore_CreateWritesMetadataLine(t *testing.T) {
	ctx := context.Background()
	s, dir := newJSONLStore(t)

	now := time.Now().Truncate(time.Second)
	require.NoError(t, s.Create(ctx, ash.SessionMeta{
		ID:        "s-1",
		CreatedAt: now,
		UpdatedAt: now,
		Metadata:  map[string]any{"model": "opus"},
	}))

	data, err := os.ReadFile(filepath.Join(dir, "s-1.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"id":"s-1"`)
	assert.Contains(t, string(data), `"opus"`)
}

func TestJSONLStore_AppendWithoutTruncation(t *testing.T) {
	ctx := context.Background()
	s, dir := newJSONLStore(t)

	now := time.Now().Truncate(time.Second)
	require.NoError(t, s.Create(ctx, ash.SessionMeta{
		ID: "s-1", CreatedAt: now, UpdatedAt: now,
	}))

	for i := range 3 {
		require.NoError(t, s.AppendTurn(ctx, "s-1", ash.Turn{
			ID:      fmt.Sprintf("t-%d", i),
			Role:    ash.RoleUser,
			Content: fmt.Sprintf("msg-%d", i),
		}))
	}

	// File should have 4 lines (1 meta + 3 turns).
	data, err := os.ReadFile(filepath.Join(dir, "s-1.jsonl"))
	require.NoError(t, err)

	lines := nonEmptyLines(data)
	assert.Len(t, lines, 4)
}

func TestJSONLStore_LoadReconstructsSession(t *testing.T) {
	ctx := context.Background()
	s, _ := newJSONLStore(t)

	now := time.Now().Truncate(time.Second)
	require.NoError(t, s.Create(ctx, ash.SessionMeta{
		ID:        "s-1",
		ParentID:  "root",
		CreatedAt: now,
		UpdatedAt: now,
	}))

	for i := range 3 {
		require.NoError(t, s.AppendTurn(ctx, "s-1", ash.Turn{
			ID:        fmt.Sprintf("t-%d", i),
			Role:      ash.RoleAssistant,
			Content:   fmt.Sprintf("resp-%d", i),
			Timestamp: now.Add(time.Duration(i) * time.Second),
		}))
	}

	sess, err := s.Load(ctx, "s-1")
	require.NoError(t, err)
	assert.Equal(t, "s-1", sess.ID)
	assert.Equal(t, "root", sess.ParentID)
	require.Len(t, sess.Turns, 3)
	assert.Equal(t, "resp-0", sess.Turns[0].Content)
	assert.Equal(t, "resp-2", sess.Turns[2].Content)
}

func TestJSONLStore_LoadNotFound(t *testing.T) {
	ctx := context.Background()
	s, _ := newJSONLStore(t)

	_, err := s.Load(ctx, "missing")
	assert.ErrorIs(t, err, ash.ErrSessionNotFound)
}

func TestJSONLStore_ListReadsMetadata(t *testing.T) {
	ctx := context.Background()
	s, _ := newJSONLStore(t)

	base := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	for i := range 3 {
		id := fmt.Sprintf("s-%d", i)
		require.NoError(t, s.Create(ctx, ash.SessionMeta{
			ID:        id,
			ParentID:  "root",
			CreatedAt: base.Add(time.Duration(i) * time.Hour),
			UpdatedAt: base.Add(time.Duration(i) * time.Hour),
		}))
		if i == 1 {
			require.NoError(t, s.AppendTurn(ctx, id, ash.Turn{
				ID: "t-0", Role: ash.RoleUser, Content: "hi",
			}))
		}
	}

	metas, err := s.List(ctx, ash.Filter{ParentID: "root"})
	require.NoError(t, err)
	assert.Len(t, metas, 3)

	// Verify turn count was derived.
	for _, m := range metas {
		if m.ID == "s-1" {
			assert.Equal(t, 1, m.TurnCount)
		}
	}
}

func TestJSONLStore_FilePerSessionIsolation(t *testing.T) {
	ctx := context.Background()
	s, _ := newJSONLStore(t)

	now := time.Now().Truncate(time.Second)
	require.NoError(t, s.Create(ctx, ash.SessionMeta{
		ID: "a", CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, s.Create(ctx, ash.SessionMeta{
		ID: "b", CreatedAt: now, UpdatedAt: now,
	}))

	require.NoError(t, s.AppendTurn(ctx, "a", ash.Turn{
		ID: "t-a", Role: ash.RoleUser, Content: "only-in-a",
	}))

	sessA, err := s.Load(ctx, "a")
	require.NoError(t, err)
	assert.Len(t, sessA.Turns, 1)

	sessB, err := s.Load(ctx, "b")
	require.NoError(t, err)
	assert.Empty(t, sessB.Turns)
}

func TestJSONLStore_Delete(t *testing.T) {
	ctx := context.Background()
	s, _ := newJSONLStore(t)

	now := time.Now()
	require.NoError(t, s.Create(ctx, ash.SessionMeta{
		ID: "s-1", CreatedAt: now, UpdatedAt: now,
	}))

	require.NoError(t, s.Delete(ctx, "s-1"))

	_, err := s.Load(ctx, "s-1")
	assert.ErrorIs(t, err, ash.ErrSessionNotFound)
}

func TestJSONLStore_DeleteNotFound(t *testing.T) {
	ctx := context.Background()
	s, _ := newJSONLStore(t)

	err := s.Delete(ctx, "missing")
	assert.ErrorIs(t, err, ash.ErrSessionNotFound)
}

func TestJSONLStore_AppendToMissing(t *testing.T) {
	ctx := context.Background()
	s, _ := newJSONLStore(t)

	err := s.AppendTurn(ctx, "missing", ash.Turn{ID: "t-0"})
	assert.ErrorIs(t, err, ash.ErrSessionNotFound)
}

// nonEmptyLines splits data by newline and returns non-empty lines.
func nonEmptyLines(data []byte) []string {
	var out []string
	for _, line := range splitLines(data) {
		if len(line) > 0 {
			out = append(out, string(line))
		}
	}
	return out
}

func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
