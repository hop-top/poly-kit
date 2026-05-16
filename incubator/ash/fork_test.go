package ash_test

import (
	"encoding/json"
	"testing"
	"time"

	"hop.top/ash"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForkCreatesSessionWithParentPointer(t *testing.T) {
	parent := &ash.Session{
		ID:        "parent-1",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Metadata:  map[string]any{"model": "claude"},
	}

	forked := parent.Fork("child-1")

	assert.Equal(t, "child-1", forked.ID)
	assert.Equal(t, "parent-1", forked.ParentID)
	assert.NotZero(t, forked.CreatedAt)
	assert.Nil(t, forked.ClosedAt)
}

func TestForkCopiesTurns(t *testing.T) {
	parent := &ash.Session{
		ID:        "p-1",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Turns: []ash.Turn{
			{
				ID:      "t-1",
				Role:    ash.RoleUser,
				Content: "hello",
				Parts: []ash.ContentPart{
					{Type: ash.PartText, Text: "hello"},
					{Type: ash.PartImage, Data: []byte{0x89, 0x50}},
				},
				ToolCalls: []ash.ToolCall{
					{
						ID:     "tc-1",
						Name:   "read",
						Input:  json.RawMessage(`{"path":"a"}`),
						Output: json.RawMessage(`{"ok":true}`),
					},
				},
				Metadata: map[string]any{"seq": float64(1)},
			},
			{
				ID:      "t-2",
				Role:    ash.RoleAssistant,
				Content: "world",
			},
		},
	}

	forked := parent.Fork("f-1")
	require.Len(t, forked.Turns, 2)

	assert.Equal(t, "t-1", forked.Turns[0].ID)
	assert.Equal(t, ash.RoleUser, forked.Turns[0].Role)
	assert.Equal(t, "hello", forked.Turns[0].Content)

	// Parts deep-copied.
	require.Len(t, forked.Turns[0].Parts, 2)
	assert.Equal(t, ash.PartImage, forked.Turns[0].Parts[1].Type)

	// ToolCalls deep-copied.
	require.Len(t, forked.Turns[0].ToolCalls, 1)
	assert.JSONEq(t, `{"path":"a"}`, string(forked.Turns[0].ToolCalls[0].Input))

	// Metadata deep-copied.
	assert.Equal(t, float64(1), forked.Turns[0].Metadata["seq"])
}

func TestForkIndependence(t *testing.T) {
	parent := &ash.Session{
		ID:        "p-1",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Metadata:  map[string]any{"k": "v"},
		Turns: []ash.Turn{
			{ID: "t-1", Role: ash.RoleUser, Content: "msg1"},
		},
	}

	forked := parent.Fork("f-1")

	// Append to fork.
	forked.Turns = append(forked.Turns, ash.Turn{
		ID: "t-new", Role: ash.RoleAssistant, Content: "reply",
	})

	// Append to parent.
	parent.Turns = append(parent.Turns, ash.Turn{
		ID: "t-p2", Role: ash.RoleUser, Content: "msg2",
	})

	// They remain independent.
	assert.Len(t, parent.Turns, 2)
	assert.Len(t, forked.Turns, 2)
	assert.Equal(t, "t-p2", parent.Turns[1].ID)
	assert.Equal(t, "t-new", forked.Turns[1].ID)

	// Metadata independence.
	forked.Metadata["k"] = "changed"
	assert.Equal(t, "v", parent.Metadata["k"])
}

func TestForkEmptySession(t *testing.T) {
	parent := &ash.Session{
		ID:        "p-empty",
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	forked := parent.Fork("f-empty")
	assert.Equal(t, "p-empty", forked.ParentID)
	assert.Empty(t, forked.Turns)
}
