package ash

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// JSONLStore persists sessions as JSONL files: one file per session.
// The first line is the session metadata; subsequent lines are turns.
type JSONLStore struct {
	dir string
}

// NewJSONLStore returns a store rooted at dir. The directory is
// created on the first write if it does not exist.
func NewJSONLStore(dir string) *JSONLStore {
	return &JSONLStore{dir: dir}
}

func (s *JSONLStore) path(id string) string {
	return filepath.Join(s.dir, id+".jsonl")
}

func (s *JSONLStore) ensureDir() error {
	return os.MkdirAll(s.dir, 0o755)
}

func (s *JSONLStore) Create(
	_ context.Context, meta SessionMeta,
) error {
	if err := s.ensureDir(); err != nil {
		return err
	}

	f, err := os.Create(s.path(meta.ID))
	if err != nil {
		return err
	}
	defer f.Close()

	return json.NewEncoder(f).Encode(meta)
}

func (s *JSONLStore) Load(
	_ context.Context, id string,
) (*Session, error) {
	f, err := os.Open(s.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0), 10*1024*1024) // 10 MB max line

	// First line: metadata.
	if !scanner.Scan() {
		return nil, ErrSessionNotFound
	}
	var meta SessionMeta
	if err := json.Unmarshal(scanner.Bytes(), &meta); err != nil {
		return nil, err
	}

	sess := &Session{
		ID:        meta.ID,
		Metadata:  meta.Metadata,
		ParentID:  meta.ParentID,
		CreatedAt: meta.CreatedAt,
		UpdatedAt: meta.UpdatedAt,
	}

	// Remaining lines: turns.
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var turn Turn
		if err := json.Unmarshal([]byte(line), &turn); err != nil {
			return nil, err
		}
		sess.Turns = append(sess.Turns, turn)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return sess, nil
}

func (s *JSONLStore) AppendTurn(
	_ context.Context, sessionID string, turn Turn,
) error {
	p := s.path(sessionID)
	if _, err := os.Stat(p); err != nil {
		if os.IsNotExist(err) {
			return ErrSessionNotFound
		}
		return err
	}

	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	return json.NewEncoder(f).Encode(turn)
}

func (s *JSONLStore) List(
	_ context.Context, f Filter,
) ([]SessionMeta, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []SessionMeta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}

		meta, err := s.readMeta(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue // skip corrupt files
		}

		if f.ParentID != "" && meta.ParentID != f.ParentID {
			continue
		}
		if !f.After.IsZero() && !meta.CreatedAt.After(f.After) {
			continue
		}
		if !f.Before.IsZero() && !meta.CreatedAt.Before(f.Before) {
			continue
		}

		out = append(out, meta)
	}

	if f.Offset > 0 && f.Offset < len(out) {
		out = out[f.Offset:]
	} else if f.Offset >= len(out) && f.Offset > 0 {
		return nil, nil
	}

	if f.Limit > 0 && f.Limit < len(out) {
		out = out[:f.Limit]
	}

	return out, nil
}

func (s *JSONLStore) readMeta(path string) (SessionMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return SessionMeta{}, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0), 10*1024*1024) // 10 MB max line
	if !scanner.Scan() {
		return SessionMeta{}, ErrSessionNotFound
	}

	var meta SessionMeta
	if err := json.Unmarshal(scanner.Bytes(), &meta); err != nil {
		return SessionMeta{}, err
	}

	// Count remaining lines for TurnCount.
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			meta.TurnCount++
		}
	}

	// Use file mod time for UpdatedAt when turns exist.
	if meta.TurnCount > 0 {
		info, err := os.Stat(path)
		if err == nil {
			meta.UpdatedAt = info.ModTime()
		}
	}

	return meta, nil
}

func (s *JSONLStore) Delete(_ context.Context, id string) error {
	p := s.path(id)
	if _, err := os.Stat(p); err != nil {
		if os.IsNotExist(err) {
			return ErrSessionNotFound
		}
		return err
	}
	return os.Remove(p)
}

// ListTurns returns turns for the given session matching the filter.
func (s *JSONLStore) ListTurns(ctx context.Context, sessionID string, f TurnFilter) ([]Turn, error) {
	sess, err := s.Load(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	var result []Turn
	for _, t := range sess.Turns {
		if f.Role != "" && t.Role != f.Role {
			continue
		}
		if !f.After.IsZero() && !t.Timestamp.After(f.After) {
			continue
		}
		if !f.Before.IsZero() && !t.Timestamp.Before(f.Before) {
			continue
		}
		result = append(result, t)
	}
	if f.Offset > 0 && f.Offset < len(result) {
		result = result[f.Offset:]
	} else if f.Offset >= len(result) {
		return nil, nil
	}
	if f.Limit > 0 && f.Limit < len(result) {
		result = result[:f.Limit]
	}
	return result, nil
}

func (s *JSONLStore) Close() error { return nil }

// compile-time interface checks
var _ Store = (*JSONLStore)(nil)
var _ Store = (*MemoryStore)(nil)
