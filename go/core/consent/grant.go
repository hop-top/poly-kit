package consent

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Capability is a namespaced consent capability id such as
// "io.screen.capture". The single-segment id "telemetry" is reserved
// for the telemetry opt-in; every other id has two or more segments.
type Capability string

// CapTelemetry is the telemetry opt-in as a capability. Its record is
// the kit.telemetry.consent block that predates grants, so it takes
// only an any-subject, always grant.
const CapTelemetry Capability = "telemetry"

const (
	maxCapabilityLen = 128
	maxSubjectLen    = 256
)

// Effect is a grant's decision.
type Effect string

const (
	Allow Effect = "allow"
	Deny  Effect = "deny"
)

// Duration is how long a decision holds.
type Duration string

const (
	// Once holds for one Use. Kept in memory only.
	Once Duration = "once"
	// Session holds until EndSession or the Grants value is dropped.
	// Kept in memory only.
	Session Duration = "session"
	// Until holds while the clock is before ExpiresAt. Persisted.
	Until Duration = "until"
	// Always holds until revoked. Persisted.
	Always Duration = "always"
)

// Persisted reports whether a grant of this duration reaches the store.
func (d Duration) Persisted() bool { return d == Until || d == Always }

// Grant is one decision about one capability for one subject. An empty
// Subject means any subject; a grant for a named subject beats it.
type Grant struct {
	Capability     Capability
	Subject        string
	Decision       Effect
	Duration       Duration
	DecidedAt      time.Time
	ExpiresAt      time.Time // set for Until only
	DecisionSource DecisionSource
	PromptVersion  int
}

// Expired reports whether an Until grant no longer holds at now.
func (g Grant) Expired(now time.Time) bool {
	return g.Duration == Until && !now.Before(g.ExpiresAt)
}

// ErrorKind names a class of grant error. The strings are the
// cross-language contract (contracts/consent-v1); messages are not.
type ErrorKind string

const (
	KindInvalidCapability ErrorKind = "invalid_capability"
	KindInvalidSubject    ErrorKind = "invalid_subject"
	KindInvalidDecision   ErrorKind = "invalid_decision"
	KindInvalidDuration   ErrorKind = "invalid_duration"
	KindInvalidExpiry     ErrorKind = "invalid_expiry"
	KindInvalidSource     ErrorKind = "invalid_source"
	KindUnsupportedScope  ErrorKind = "unsupported_scope"
	KindMalformedStore    ErrorKind = "malformed_store"
)

// GrantError is a grant validation or store error of one Kind.
// errors.Is matches the Err* sentinel of the same kind.
type GrantError struct {
	Kind ErrorKind
	Msg  string
	Err  error
}

func (e *GrantError) Error() string {
	msg := "consent: " + string(e.Kind)
	if e.Msg != "" {
		msg += ": " + e.Msg
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *GrantError) Unwrap() error { return e.Err }

// Is matches a sentinel (no message) of the same kind.
func (e *GrantError) Is(target error) bool {
	t, ok := target.(*GrantError)
	return ok && t.Msg == "" && t.Err == nil && t.Kind == e.Kind
}

// Sentinels for errors.Is.
var (
	ErrInvalidCapability = &GrantError{Kind: KindInvalidCapability}
	ErrInvalidSubject    = &GrantError{Kind: KindInvalidSubject}
	ErrInvalidDecision   = &GrantError{Kind: KindInvalidDecision}
	ErrInvalidDuration   = &GrantError{Kind: KindInvalidDuration}
	ErrInvalidExpiry     = &GrantError{Kind: KindInvalidExpiry}
	ErrInvalidSource     = &GrantError{Kind: KindInvalidSource}
	ErrUnsupportedScope  = &GrantError{Kind: KindUnsupportedScope}
	ErrMalformedStore    = &GrantError{Kind: KindMalformedStore}
)

// KindOf returns the ErrorKind of err, or "" when err is not a
// GrantError.
func KindOf(err error) ErrorKind {
	var ge *GrantError
	if errors.As(err, &ge) {
		return ge.Kind
	}
	return ""
}

func grantErr(kind ErrorKind, format string, args ...any) error {
	return &GrantError{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// ValidateCapability checks id against the capability grammar.
func ValidateCapability(id string) error {
	if id == string(CapTelemetry) {
		return nil
	}
	if len(id) > maxCapabilityLen {
		return grantErr(KindInvalidCapability, "%q longer than %d bytes", id, maxCapabilityLen)
	}
	segs := strings.Split(id, ".")
	if len(segs) < 2 {
		return grantErr(KindInvalidCapability, "%q is not namespaced (want two or more dot-separated segments)", id)
	}
	for _, s := range segs {
		if !validSegment(s) {
			return grantErr(KindInvalidCapability, "%q: segment %q must match ^[a-z][a-z0-9_]*$", id, s)
		}
	}
	return nil
}

func validSegment(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

// ValidateSubject checks a subject: empty (any subject), or 1-256
// bytes of UTF-8 with no whitespace or control characters.
func ValidateSubject(s string) error {
	if s == "" {
		return nil
	}
	if len(s) > maxSubjectLen {
		return grantErr(KindInvalidSubject, "longer than %d bytes", maxSubjectLen)
	}
	if !utf8.ValidString(s) {
		return grantErr(KindInvalidSubject, "%q is not valid UTF-8", s)
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return grantErr(KindInvalidSubject, "%q contains whitespace or a control character", s)
		}
	}
	return nil
}

func validateKey(c Capability, subject string) error {
	if err := ValidateCapability(string(c)); err != nil {
		return err
	}
	return ValidateSubject(subject)
}

// Validate checks every field of g. DecidedAt must be set.
func (g Grant) Validate() error {
	if err := validateKey(g.Capability, g.Subject); err != nil {
		return err
	}
	if g.Decision != Allow && g.Decision != Deny {
		return grantErr(KindInvalidDecision, "%q (want allow or deny)", g.Decision)
	}
	switch g.Duration {
	case Once, Session, Always:
		if !g.ExpiresAt.IsZero() {
			return grantErr(KindInvalidExpiry, "expires_at is only valid for until")
		}
	case Until:
		if g.ExpiresAt.IsZero() {
			return grantErr(KindInvalidExpiry, "until needs expires_at")
		}
		if !g.ExpiresAt.After(g.DecidedAt) {
			return grantErr(KindInvalidExpiry, "expires_at must be after decided_at")
		}
	default:
		return grantErr(KindInvalidDuration, "%q (want once, session, until or always)", g.Duration)
	}
	if !validSource(g.DecisionSource) {
		return grantErr(KindInvalidSource, "%q (want prompt, flag, env or config)", g.DecisionSource)
	}
	if g.PromptVersion < 0 {
		return grantErr(KindInvalidDecision, "prompt_version %d is negative", g.PromptVersion)
	}
	if g.Capability == CapTelemetry && (g.Subject != "" || g.Duration != Always) {
		return grantErr(KindUnsupportedScope, "telemetry takes only an any-subject always grant")
	}
	if g.DecidedAt.IsZero() {
		return grantErr(KindInvalidDecision, "decided_at is required")
	}
	return nil
}

func validSource(s DecisionSource) bool {
	switch s {
	case SourcePrompt, SourceFlag, SourceEnv, SourceConfig:
		return true
	}
	return false
}

type grantKey struct {
	cap     Capability
	subject string
}

func (g Grant) key() grantKey { return grantKey{g.Capability, g.Subject} }

// telemetryGrant maps the telemetry Decision record to its grant.
func telemetryGrant(d Decision) (Grant, bool) {
	var e Effect
	switch d.State {
	case StateGranted:
		e = Allow
	case StateDenied:
		e = Deny
	default:
		return Grant{}, false
	}
	return Grant{
		Capability:     CapTelemetry,
		Decision:       e,
		Duration:       Always,
		DecidedAt:      d.DecidedAt,
		DecisionSource: d.DecisionSource,
		PromptVersion:  d.PromptVersion,
	}, true
}

// telemetryDecision maps a telemetry grant back to the Decision record.
func telemetryDecision(g Grant) Decision {
	s := StateDenied
	if g.Decision == Allow {
		s = StateGranted
	}
	return Decision{
		State:          s,
		DecidedAt:      g.DecidedAt,
		PromptVersion:  g.PromptVersion,
		DecisionSource: g.DecisionSource,
	}
}
