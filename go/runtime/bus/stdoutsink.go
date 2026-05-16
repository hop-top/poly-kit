package bus

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
)

const payloadMaxLen = 120

// StdoutSink writes human-readable formatted events to an io.Writer.
type StdoutSink struct {
	mu sync.Mutex
	w  io.Writer
}

// NewStdoutSink returns a StdoutSink that writes to os.Stdout.
func NewStdoutSink() *StdoutSink {
	return &StdoutSink{w: os.Stdout}
}

// NewStdoutSinkWriter returns a StdoutSink that writes to w.
func NewStdoutSinkWriter(w io.Writer) *StdoutSink {
	return &StdoutSink{w: w}
}

// Drain formats and writes the event to the underlying writer.
// Format: [2006-01-02T15:04:05] topic source: payload_summary
func (s *StdoutSink) Drain(_ context.Context, e Event) error {
	summary := truncate(fmt.Sprintf("%v", e.Payload), payloadMaxLen)

	line := fmt.Sprintf("[%s] %s %s: %s\n",
		e.Timestamp.Format("2006-01-02T15:04:05"),
		e.Topic,
		e.Source,
		summary,
	)

	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := io.WriteString(s.w, line)
	return err
}

// Close is a no-op; StdoutSink does not own the underlying writer.
func (s *StdoutSink) Close() error {
	return nil
}

// truncate returns str if it fits in max runes, or the first max runes
// followed by "..." if it exceeds max.
func truncate(str string, max int) string {
	runes := []rune(str)
	if len(runes) <= max {
		return str
	}
	return string(runes[:max]) + "..."
}
