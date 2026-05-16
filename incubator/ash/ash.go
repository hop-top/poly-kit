// Package ash provides core types for AI session history.
package ash

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// Sentinel errors.
var (
	ErrSessionNotFound = errors.New("ash: session not found")
	ErrSessionClosed   = errors.New("ash: session is closed")
)

// Role identifies the author of a turn.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
	RoleAgent     Role = "agent"
)

// PartType classifies content within a turn.
type PartType string

const (
	PartText  PartType = "text"
	PartImage PartType = "image"
	PartAudio PartType = "audio"
	PartVideo PartType = "video"
)

// ContentPart is one piece of multimodal content within a turn.
type ContentPart struct {
	Type     PartType `json:"type"`
	Text     string   `json:"text,omitempty"`
	MimeType string   `json:"mime_type,omitempty"`
	Data     []byte   `json:"data,omitempty"`
}

// ToolCall records a single tool invocation and its result.
type ToolCall struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input,omitempty"`
	Output   json.RawMessage `json:"output,omitempty"`
	Status   string          `json:"status,omitempty"`
	Duration time.Duration   `json:"duration,omitempty"`
}

// Turn is one message in a session.
type Turn struct {
	ID        string         `json:"id"`
	Role      Role           `json:"role"`
	Content   string         `json:"content,omitempty"`
	Parts     []ContentPart  `json:"parts,omitempty"`
	ToolCalls []ToolCall     `json:"tool_calls,omitempty"`
	ParentID  string         `json:"parent_id,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Seq       int            `json:"seq,omitempty"`
}

// Session holds a complete conversation with all its turns.
type Session struct {
	ID        string         `json:"id"`
	Turns     []Turn         `json:"turns"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	ParentID  string         `json:"parent_id,omitempty"`
	Children  []string       `json:"children,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	ClosedAt  *time.Time     `json:"closed_at,omitempty"`

	// Runtime (unexported, not serialized).
	mu       sync.Mutex `json:"-"`
	provider Provider   `json:"-"`
	store    Store      `json:"-"`
	pub      Publisher  `json:"-"`
	router   Router     `json:"-"`
}

// SessionMeta is a lightweight summary for listing sessions
// without loading full turn data.
type SessionMeta struct {
	ID        string         `json:"id"`
	TurnCount int            `json:"turn_count"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	ParentID  string         `json:"parent_id,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// Filter constrains session listing queries.
type Filter struct {
	Before   time.Time `json:"before,omitempty"`
	After    time.Time `json:"after,omitempty"`
	ParentID string    `json:"parent_id,omitempty"`
	Limit    int       `json:"limit,omitempty"`
	Offset   int       `json:"offset,omitempty"`
}

// TurnFilter constrains turn listing queries.
type TurnFilter struct {
	Role   Role      `json:"role,omitempty"`
	After  time.Time `json:"after,omitempty"`
	Before time.Time `json:"before,omitempty"`
	Limit  int       `json:"limit,omitempty"`
	Offset int       `json:"offset,omitempty"`
}

// Router delivers turns between sessions.
type Router interface {
	Route(ctx context.Context, fromID, toID string, turn Turn) error
}

// Store is the persistence interface for sessions and turns.
//
// List ordering is implementation-defined: MemoryStore returns oldest
// first (ASC); SQLite returns newest first (DESC). Use Filter.OrderBy
// when deterministic ordering matters.
type Store interface {
	Create(ctx context.Context, meta SessionMeta) error
	Load(ctx context.Context, id string) (*Session, error)
	AppendTurn(ctx context.Context, sessionID string, turn Turn) error
	List(ctx context.Context, f Filter) ([]SessionMeta, error)
	ListTurns(ctx context.Context, sessionID string, f TurnFilter) ([]Turn, error)
	Delete(ctx context.Context, id string) error
	Close() error
}

// Fork creates a new Session derived from s. The forked session gets
// a deep copy of all current turns; subsequent mutations to either
// session are independent.
func (s *Session) Fork(id string) *Session {
	now := time.Now().UTC()
	turns := make([]Turn, len(s.Turns))
	for i, t := range s.Turns {
		turns[i] = deepCopyTurn(t)
	}

	meta := deepCopyMeta(s.Metadata)

	return &Session{
		ID:        id,
		Turns:     turns,
		Metadata:  meta,
		ParentID:  s.ID,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func deepCopyTurn(t Turn) Turn {
	cp := t
	if t.Parts != nil {
		cp.Parts = make([]ContentPart, len(t.Parts))
		for i, p := range t.Parts {
			cp.Parts[i] = p
			if p.Data != nil {
				cp.Parts[i].Data = make([]byte, len(p.Data))
				copy(cp.Parts[i].Data, p.Data)
			}
		}
	}
	if t.ToolCalls != nil {
		cp.ToolCalls = make([]ToolCall, len(t.ToolCalls))
		for i, tc := range t.ToolCalls {
			cp.ToolCalls[i] = tc
			if tc.Input != nil {
				cp.ToolCalls[i].Input = make(json.RawMessage, len(tc.Input))
				copy(cp.ToolCalls[i].Input, tc.Input)
			}
			if tc.Output != nil {
				cp.ToolCalls[i].Output = make(json.RawMessage, len(tc.Output))
				copy(cp.ToolCalls[i].Output, tc.Output)
			}
		}
	}
	if t.Metadata != nil {
		cp.Metadata = make(map[string]any, len(t.Metadata))
		for k, v := range t.Metadata {
			cp.Metadata[k] = v
		}
	}
	return cp
}

func deepCopyMeta(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	cp := make(map[string]any, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}
