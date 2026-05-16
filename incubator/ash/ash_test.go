package ash_test

import (
	"encoding/json"
	"testing"
	"time"

	"hop.top/ash"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTurnJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	turn := ash.Turn{
		ID:        "t-1",
		Role:      ash.RoleAssistant,
		Content:   "hello",
		ParentID:  "t-0",
		Timestamp: now,
		Metadata:  map[string]any{"key": "val"},
		ToolCalls: []ash.ToolCall{
			{
				ID:     "tc-1",
				Name:   "read",
				Input:  json.RawMessage(`{"path":"foo"}`),
				Output: json.RawMessage(`{"ok":true}`),
				Status: "success",
			},
		},
	}

	data, err := json.Marshal(turn)
	require.NoError(t, err)

	var got ash.Turn
	require.NoError(t, json.Unmarshal(data, &got))

	assert.Equal(t, turn.ID, got.ID)
	assert.Equal(t, turn.Role, got.Role)
	assert.Equal(t, turn.Content, got.Content)
	assert.Equal(t, turn.ParentID, got.ParentID)
	assert.Equal(t, turn.Timestamp.Unix(), got.Timestamp.Unix())
	assert.Equal(t, turn.Metadata, got.Metadata)
	assert.Len(t, got.ToolCalls, 1)
	assert.Equal(t, "read", got.ToolCalls[0].Name)
	assert.JSONEq(t, `{"path":"foo"}`, string(got.ToolCalls[0].Input))
}

func TestSessionAppendAndReadOrdering(t *testing.T) {
	s := ash.Session{
		ID:        "s-1",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	for i := range 5 {
		s.Turns = append(s.Turns, ash.Turn{
			ID:        "t-" + string(rune('0'+i)),
			Role:      ash.RoleUser,
			Content:   "msg",
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	assert.Len(t, s.Turns, 5)
	for i := 1; i < len(s.Turns); i++ {
		assert.True(t, s.Turns[i].Timestamp.After(s.Turns[i-1].Timestamp),
			"turns must be in chronological order")
	}
}

func TestContentPartMultimodalSerialization(t *testing.T) {
	parts := []ash.ContentPart{
		{Type: ash.PartText, Text: "describe this"},
		{Type: ash.PartImage, MimeType: "image/png", Data: []byte{0x89, 0x50, 0x4E, 0x47}},
		{Type: ash.PartAudio, MimeType: "audio/wav", Data: []byte{0x52, 0x49}},
		{Type: ash.PartVideo, MimeType: "video/mp4", Data: []byte{0x00, 0x00}},
	}

	data, err := json.Marshal(parts)
	require.NoError(t, err)

	var got []ash.ContentPart
	require.NoError(t, json.Unmarshal(data, &got))

	require.Len(t, got, 4)
	assert.Equal(t, ash.PartText, got[0].Type)
	assert.Equal(t, "describe this", got[0].Text)
	assert.Empty(t, got[0].Data)

	assert.Equal(t, ash.PartImage, got[1].Type)
	assert.Equal(t, "image/png", got[1].MimeType)
	assert.Equal(t, []byte{0x89, 0x50, 0x4E, 0x47}, got[1].Data)

	assert.Equal(t, ash.PartAudio, got[2].Type)
	assert.Equal(t, ash.PartVideo, got[3].Type)
}

func TestSessionMetaJSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	meta := ash.SessionMeta{
		ID:        "s-1",
		TurnCount: 10,
		CreatedAt: now,
		UpdatedAt: now,
		ParentID:  "s-0",
		Metadata:  map[string]any{"model": "claude"},
	}

	data, err := json.Marshal(meta)
	require.NoError(t, err)

	var got ash.SessionMeta
	require.NoError(t, json.Unmarshal(data, &got))

	assert.Equal(t, meta.ID, got.ID)
	assert.Equal(t, meta.TurnCount, got.TurnCount)
	assert.Equal(t, meta.ParentID, got.ParentID)
	assert.Equal(t, "claude", got.Metadata["model"])
}

func TestRoleConstants(t *testing.T) {
	roles := []ash.Role{
		ash.RoleUser,
		ash.RoleAssistant,
		ash.RoleSystem,
		ash.RoleTool,
		ash.RoleAgent,
	}
	assert.Len(t, roles, 5)
	for _, r := range roles {
		assert.NotEmpty(t, r)
	}
}
