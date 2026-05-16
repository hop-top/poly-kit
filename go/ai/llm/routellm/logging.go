package routellm

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// RouteEvent records a single routing decision.
type RouteEvent struct {
	Router    string        `json:"router"`
	Score     float64       `json:"score"`
	Model     string        `json:"model"`
	Duration  time.Duration `json:"duration_ns"`
	Timestamp time.Time     `json:"timestamp"`
}

// EvaEvent records a single eva contract evaluation.
type EvaEvent struct {
	Contract   string    `json:"contract"`
	Passed     bool      `json:"passed"`
	Violations []string  `json:"violations,omitempty"`
	Confidence float64   `json:"confidence"`
	Timestamp  time.Time `json:"timestamp"`
}

// Logger defines the interface for structured router event logging.
type Logger interface {
	LogRoute(RouteEvent)
	LogEva(EvaEvent)
}

// JSONLogger writes JSON-lines to an io.Writer. Safe for concurrent use.
type JSONLogger struct {
	mu  sync.Mutex
	enc *json.Encoder
}

// NewJSONLogger creates a JSONLogger that writes to w.
func NewJSONLogger(w io.Writer) *JSONLogger {
	return &JSONLogger{enc: json.NewEncoder(w)}
}

// LogRoute writes a RouteEvent as a single JSON line.
func (l *JSONLogger) LogRoute(e RouteEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.enc.Encode(struct {
		Type string `json:"type"`
		RouteEvent
	}{Type: "route", RouteEvent: e})
}

// LogEva writes an EvaEvent as a single JSON line.
func (l *JSONLogger) LogEva(e EvaEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.enc.Encode(struct {
		Type string `json:"type"`
		EvaEvent
	}{Type: "eva", EvaEvent: e})
}
