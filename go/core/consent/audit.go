package consent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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
	// AuditRefused is a refusal that was not a decision: nothing asked,
	// nothing could be asked, or nothing could be read or recorded.
	// A "no" the user gives is AuditDenied.
	AuditRefused AuditKind = "refused"
)

// RefusalCause says why a refusal was not a decision.
type RefusalCause string

const (
	CauseNoPrompter      RefusalCause = "no-prompter"
	CausePrompterError   RefusalCause = "prompter-error"
	CauseStoreUnreadable RefusalCause = "store-unreadable"
	CauseAuditFailed     RefusalCause = "audit-failed"
	CauseStandingDeny    RefusalCause = "standing-deny"
	CauseExpired         RefusalCause = "expired"
)

// AuditEvent is one audit record: who (Subject), when (At), what
// (Event, Capability, Decision, Duration) and how it was decided
// (DecisionSource, PromptVersion). Revoked events carry no decision,
// duration or prompt version; DecisionSource is who revoked. Refused
// events carry Cause and the DecisionSource of the governing grant
// (config when none).
//
// Log and Seq are set by a durable auditor (FileAuditor): Seq counts
// 1, 2, 3, ... within Log, and "<Log>:<Seq>" is the idempotency key the
// Relay publishes as the bus event id.
type AuditEvent struct {
	Log            string
	Seq            uint64
	Event          AuditKind
	At             time.Time
	Capability     Capability
	Subject        string
	Decision       Effect
	Duration       Duration
	ExpiresAt      time.Time
	DecisionSource DecisionSource
	PromptVersion  int
	Cause          RefusalCause

	// raw is the record as read from the log. A record read back
	// marshals to it byte for byte, so fields and values a newer writer
	// added reach the bus unchanged.
	raw []byte
}

// Key returns the idempotency key "<Log>:<Seq>", or "" for an event no
// durable auditor recorded.
func (e AuditEvent) Key() string {
	if e.Log == "" || e.Seq == 0 {
		return ""
	}
	return fmt.Sprintf("%s:%d", e.Log, e.Seq)
}

const auditVersion = 1

type auditWire struct {
	V              int            `json:"v"`
	Log            string         `json:"log,omitempty"`
	Seq            uint64         `json:"seq,omitempty"`
	Event          AuditKind      `json:"event"`
	At             string         `json:"at"`
	Capability     Capability     `json:"capability"`
	Subject        string         `json:"subject"`
	Decision       Effect         `json:"decision,omitempty"`
	Duration       Duration       `json:"duration,omitempty"`
	ExpiresAt      string         `json:"expires_at,omitempty"`
	DecisionSource DecisionSource `json:"decision_source,omitempty"`
	PromptVersion  *int           `json:"prompt_version,omitempty"`
	Reason         RefusalCause   `json:"reason,omitempty"`
}

// MarshalJSON writes the contracts/consent-v1 audit shape; a record
// read back from the log is written as read.
func (e AuditEvent) MarshalJSON() ([]byte, error) {
	if len(e.raw) > 0 {
		return append([]byte(nil), e.raw...), nil
	}
	w := auditWire{
		V: auditVersion, Log: e.Log, Seq: e.Seq, Event: e.Event, At: formatTime(e.At),
		Capability: e.Capability, Subject: e.Subject,
		Decision: e.Decision, Duration: e.Duration, DecisionSource: e.DecisionSource,
		Reason: e.Cause,
	}
	if !e.ExpiresAt.IsZero() {
		w.ExpiresAt = formatTime(e.ExpiresAt)
	}
	if e.Event != AuditRevoked && e.Event != AuditRefused {
		pv := e.PromptVersion
		w.PromptVersion = &pv
	}
	return json.Marshal(w)
}

// UnmarshalJSON reads the audit shape back (the relay reads the log).
func (e *AuditEvent) UnmarshalJSON(b []byte) error {
	var w auditWire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	if w.V != auditVersion {
		return fmt.Errorf("consent: audit record version %d (want %d)", w.V, auditVersion)
	}
	if err := checkEventName(w.Event); err != nil {
		return err
	}
	at, err := time.Parse(time.RFC3339, w.At)
	if err != nil {
		return fmt.Errorf("consent: audit record at: %w", err)
	}
	out := AuditEvent{
		Log: w.Log, Seq: w.Seq, Event: w.Event, At: at, Capability: w.Capability,
		Subject: w.Subject, Decision: w.Decision, Duration: w.Duration,
		DecisionSource: w.DecisionSource, Cause: w.Reason,
	}
	if w.ExpiresAt != "" {
		if out.ExpiresAt, err = time.Parse(time.RFC3339, w.ExpiresAt); err != nil {
			return fmt.Errorf("consent: audit record expires_at: %w", err)
		}
	}
	if w.PromptVersion != nil {
		out.PromptVersion = *w.PromptVersion
	}
	out.raw = append([]byte(nil), b...)
	*e = out
	return nil
}

// knownKinds are the audit kinds this version writes.
var knownKinds = map[AuditKind]bool{
	AuditGranted: true, AuditDenied: true, AuditRevoked: true, AuditUsed: true, AuditRefused: true,
}

// checkEventName accepts a known kind, or an unknown one (a newer
// writer's) whose topic kit.core.consent.<event> passes the bus topic
// grammar: bus.Validate (four ^[a-z][a-z0-9_]*$ segments, <= 128 bytes)
// and bus.ValidateTopic (a past-tense action). Anything else makes the
// record malformed, so a hostile line cannot invent a topic.
func checkEventName(k AuditKind) error {
	if knownKinds[k] {
		return nil
	}
	t := eventTopic(k)
	if err := bus.Validate(t); err != nil {
		return fmt.Errorf("consent: audit record event %q: %w", k, err)
	}
	if err := bus.ValidateTopic(t); err != nil {
		return fmt.Errorf("consent: audit record event %q: %w", k, err)
	}
	return nil
}

// eventTopic is kit.core.consent.<event>.
func eventTopic(k AuditKind) bus.Topic { return bus.Topic("kit.core.consent." + string(k)) }

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

// Topics are the bus topics per audit kind.
type Topics struct {
	Granted bus.Topic
	Denied  bus.Topic
	Revoked bus.Topic
	Used    bus.Topic
	Refused bus.Topic
}

// DefaultTopics is the kit baseline.
var DefaultTopics = Topics{
	Granted: "kit.core.consent.granted",
	Denied:  "kit.core.consent.denied",
	Revoked: "kit.core.consent.revoked",
	Used:    "kit.core.consent.used",
	Refused: "kit.core.consent.refused",
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
	case AuditRefused:
		if t.Refused == "" {
			return DefaultTopics.Refused
		}
		return t.Refused
	case AuditUsed:
		return t.Used
	default:
		// A newer writer's event, checked against the topic grammar
		// when it was read: its own topic, never mapped to another.
		return eventTopic(k)
	}
}

// BusAuditor publishes each event inline with the AuditEvent as
// payload: delivery is confirmed by the publisher's return, but nothing
// is kept for redelivery. For durable, at-least-once delivery record to
// a FileAuditor and run a Relay instead. A publisher error (a veto from
// a pre-hook included) refuses the use.
func BusAuditor(pub domain.EventPublisher, t Topics) Auditor {
	return AuditorFunc(func(ctx context.Context, e AuditEvent) error {
		return pub.Publish(ctx, string(t.topic(e.Event)), eventSource, e)
	})
}
