package consent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"hop.top/kit/go/core/xdg"
	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/domain"
)

// AuditKind is the audited moment. The strings double as the last
// segment of the bus topics.
type AuditKind string

const (
	AuditGranted AuditKind = "granted"
	AuditDenied  AuditKind = "denied"
	AuditRevoked AuditKind = "revoked"
	AuditUsed    AuditKind = "used"
)

// AuditEvent is one audit record: who (Subject), when (At), what
// (Event, Capability, Decision, Duration) and how it was decided
// (DecisionSource, PromptVersion). Revoked events carry no decision,
// duration or prompt version; DecisionSource is who revoked.
type AuditEvent struct {
	Event          AuditKind
	At             time.Time
	Capability     Capability
	Subject        string
	Decision       Effect
	Duration       Duration
	ExpiresAt      time.Time
	DecisionSource DecisionSource
	PromptVersion  int
}

const auditVersion = 1

// MarshalJSON writes the contracts/consent-v1 audit shape.
func (e AuditEvent) MarshalJSON() ([]byte, error) {
	type wire struct {
		V              int            `json:"v"`
		Event          AuditKind      `json:"event"`
		At             string         `json:"at"`
		Capability     Capability     `json:"capability"`
		Subject        string         `json:"subject"`
		Decision       Effect         `json:"decision,omitempty"`
		Duration       Duration       `json:"duration,omitempty"`
		ExpiresAt      string         `json:"expires_at,omitempty"`
		DecisionSource DecisionSource `json:"decision_source,omitempty"`
		PromptVersion  *int           `json:"prompt_version,omitempty"`
	}
	w := wire{
		V: auditVersion, Event: e.Event, At: formatTime(e.At),
		Capability: e.Capability, Subject: e.Subject,
		Decision: e.Decision, Duration: e.Duration, DecisionSource: e.DecisionSource,
	}
	if !e.ExpiresAt.IsZero() {
		w.ExpiresAt = formatTime(e.ExpiresAt)
	}
	if e.Event != AuditRevoked {
		pv := e.PromptVersion
		w.PromptVersion = &pv
	}
	return json.Marshal(w)
}

func auditFromGrant(kind AuditKind, g Grant, at time.Time) AuditEvent {
	return AuditEvent{
		Event: kind, At: at, Capability: g.Capability, Subject: g.Subject,
		Decision: g.Decision, Duration: g.Duration, ExpiresAt: g.ExpiresAt,
		DecisionSource: g.DecisionSource, PromptVersion: g.PromptVersion,
	}
}

// Auditor records audit events. An error from Record on a use refuses
// the use: no audit entry, no tap.
type Auditor interface {
	Record(ctx context.Context, e AuditEvent) error
}

// AuditorFunc adapts a function to Auditor.
type AuditorFunc func(ctx context.Context, e AuditEvent) error

func (f AuditorFunc) Record(ctx context.Context, e AuditEvent) error { return f(ctx, e) }

// MultiAuditor records to each auditor in order and stops at the first
// error.
func MultiAuditor(as ...Auditor) Auditor {
	return AuditorFunc(func(ctx context.Context, e AuditEvent) error {
		for _, a := range as {
			if err := a.Record(ctx, e); err != nil {
				return err
			}
		}
		return nil
	})
}

// FileAuditor appends audit events as JSON lines, perms 0600.
type FileAuditor struct {
	path string
	mu   sync.Mutex
}

// NewFileAuditor writes to <XDG_STATE_HOME>/kit/consent/audit.jsonl.
func NewFileAuditor() (*FileAuditor, error) {
	p, err := xdg.StateFile(xdgTool, filepath.Join("consent", "audit.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("consent: resolve audit path: %w", err)
	}
	return NewFileAuditorAt(p), nil
}

// NewFileAuditorAt writes to path.
func NewFileAuditorAt(path string) *FileAuditor { return &FileAuditor{path: path} }

// Path returns the audit log path.
func (a *FileAuditor) Path() string { return a.path }

func (a *FileAuditor) Record(ctx context.Context, e AuditEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(a.path), dirPerm); err != nil {
		return fmt.Errorf("consent: audit mkdir: %w", err)
	}
	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, filePerm)
	if err != nil {
		return fmt.Errorf("consent: audit open: %w", err)
	}
	if _, err := f.Write(line); err != nil {
		_ = f.Close()
		return fmt.Errorf("consent: audit write: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("consent: audit sync: %w", err)
	}
	return f.Close()
}

// Topics are the bus topics per audit kind.
type Topics struct {
	Granted bus.Topic
	Denied  bus.Topic
	Revoked bus.Topic
	Used    bus.Topic
}

// DefaultTopics is the kit baseline.
var DefaultTopics = Topics{
	Granted: "kit.core.consent.granted",
	Denied:  "kit.core.consent.denied",
	Revoked: "kit.core.consent.revoked",
	Used:    "kit.core.consent.used",
}

const eventSource = "core.consent"

func (t Topics) topic(k AuditKind) bus.Topic {
	switch k {
	case AuditGranted:
		return t.Granted
	case AuditDenied:
		return t.Denied
	case AuditRevoked:
		return t.Revoked
	default:
		return t.Used
	}
}

// BusAuditor publishes each event with the AuditEvent as payload. A
// publisher error (a veto from a pre-hook included) refuses the use.
func BusAuditor(pub domain.EventPublisher, t Topics) Auditor {
	return AuditorFunc(func(ctx context.Context, e AuditEvent) error {
		return pub.Publish(ctx, string(t.topic(e.Event)), eventSource, e)
	})
}
