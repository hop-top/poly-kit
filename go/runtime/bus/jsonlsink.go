package bus

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

// jsonlEvent is the serialization shape for one JSONL line.
type jsonlEvent struct {
	Topic     string `json:"topic"`
	Source    string `json:"source"`
	Timestamp string `json:"timestamp"`
	Payload   any    `json:"payload,omitempty"`
}

// JSONLSink writes events as newline-delimited JSON to an io.Writer.
type JSONLSink struct {
	mu     sync.Mutex
	w      *bufio.Writer
	closer io.Closer // non-nil only when we own the file
	closed bool
}

// compile-time interface check
var _ Sink = (*JSONLSink)(nil)

// NewJSONLSink returns a JSONLSink that writes to w.
// The caller retains ownership of w; Close flushes but does not close it.
func NewJSONLSink(w io.Writer) *JSONLSink {
	return &JSONLSink{w: bufio.NewWriter(w)}
}

// NewJSONLSinkFile opens (or creates) the file at path in append mode and
// returns a JSONLSink that owns the file handle. Close flushes and closes it.
func NewJSONLSinkFile(path string) (*JSONLSink, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &JSONLSink{
		w:      bufio.NewWriter(f),
		closer: f,
	}, nil
}

// Drain marshals e as a single JSON line and writes it.
func (s *JSONLSink) Drain(_ context.Context, e Event) error {
	rec := jsonlEvent{
		Topic:     string(e.Topic),
		Source:    e.Source,
		Timestamp: e.Timestamp.Format(time.RFC3339Nano),
		Payload:   e.Payload,
	}

	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return os.ErrClosed
	}

	if _, err := s.w.Write(data); err != nil {
		return err
	}
	return s.w.WriteByte('\n')
}

// Close flushes buffered data and, if the sink owns the underlying file,
// closes it. Close is idempotent.
func (s *JSONLSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	if err := s.w.Flush(); err != nil {
		return err
	}
	if s.closer != nil {
		return s.closer.Close()
	}
	return nil
}
