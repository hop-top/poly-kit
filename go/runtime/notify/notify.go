// Package notify provides transport-agnostic notification primitives
// built on top of go/runtime/bus.Sink.
//
// The package layers four concerns on the bus.Sink interface, each
// implemented as a Sink decorator so they compose freely:
//
//   - Severity convention. A small Severity type + the WithSeverity
//     optional interface + the SeverityOf(bus.Event) helper let
//     payloads advertise urgency without forcing a payload-schema
//     migration. See notify.go.
//   - Filter decorator. FilterSink wraps any Sink and drops events
//     that fail a topic pattern, severity floor, or arbitrary
//     predicate. See filter.go.
//   - Retry + dead-letter (P2). RetrySink wraps any Sink with at-
//     least-once delivery semantics; exhausted attempts route to a
//     configurable dead-letter Sink. (Not in this file; landing in
//     phase 2.)
//   - Reference sinks (P3). Out-of-tree subpackages
//     (sinks/webhook, sinks/email, sinks/osnotify) implement the
//     three transports the catalog ships. See guardrails.go for the
//     WithRedactor + WithBreaker convention every outbound sink
//     adopts.
//
// Cross-language parity. The MVP is Go-only. bus.Sink and bus.TeeBus
// themselves are still Go-only (TS/Python ports exist for pub/sub
// only); notify ports are gated on those primitives porting first
// and tracked under kit-notify-polyglot. See spec §3 #8 and §11.
//
// Spec: docs/specs/notifications.md.
package notify

import (
	"strings"

	"hop.top/kit/go/runtime/bus"
)

// Severity classifies an event for notification routing decisions.
// SeverityInfo is the default when an event payload does not
// advertise severity. The numeric ordering is Debug < Info < Warn <
// Error < Critical so callers can compare with > and >= against a
// configured floor (see WithMinSeverity in filter.go).
type Severity int

const (
	// SeverityDebug is fine-grained development trace; usually muted
	// in production filters.
	SeverityDebug Severity = iota
	// SeverityInfo is routine operational events. Default when a
	// payload does not advertise severity.
	SeverityInfo
	// SeverityWarn is degradation worth a heads-up but not a page.
	SeverityWarn
	// SeverityError is a failure that needs attention; typical
	// e-mail/chat threshold.
	SeverityError
	// SeverityCritical is page-now urgency; typical PagerDuty/SMS
	// threshold.
	SeverityCritical
)

// String returns the lowercase human form ("debug", "info", "warn",
// "error", "critical"). Used in tests, log attrs, and notification
// templates.
func (s Severity) String() string {
	switch s {
	case SeverityDebug:
		return "debug"
	case SeverityInfo:
		return "info"
	case SeverityWarn:
		return "warn"
	case SeverityError:
		return "error"
	case SeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// WithSeverity is the optional interface that typed payloads can
// implement to advertise severity in-process without a JSON
// round-trip. SeverityOf checks for this interface first, before
// falling back to map-based lookup.
type WithSeverity interface {
	Severity() Severity
}

// SeverityOf reads severity from an event's payload.
//
// Resolution order:
//
//  1. e.Payload satisfies WithSeverity → return p.Severity().
//  2. e.Payload is a map[string]any with a "severity" key whose
//     value is a string ("debug" / "info" / "warn" / "error" /
//     "critical") or a number (int / int64 / float64) in
//     [SeverityDebug, SeverityCritical] → use that.
//  3. Otherwise → SeverityInfo.
//
// Cross-process payload semantics. bus events may be delivered as
// map[string]any after JSON decode (see the type-erasure warning in
// bus/event.go). SeverityOf handles both the in-process typed shape
// and the post-decode map shape uniformly.
//
// Keyword matching is case-sensitive, lowercase only — consistent
// with the wire contract emitters use when serializing the
// "severity" field. Anything else (unknown keyword, out-of-range
// number, missing key, nil payload) returns SeverityInfo.
func SeverityOf(e bus.Event) Severity {
	if e.Payload == nil {
		return SeverityInfo
	}
	if ws, ok := e.Payload.(WithSeverity); ok {
		return ws.Severity()
	}
	m, ok := e.Payload.(map[string]any)
	if !ok {
		return SeverityInfo
	}
	v, ok := m["severity"]
	if !ok {
		return SeverityInfo
	}
	switch x := v.(type) {
	case string:
		return severityFromKeyword(x)
	case int:
		return severityFromInt(int64(x))
	case int64:
		return severityFromInt(x)
	case float64:
		// JSON-decoded numbers come through as float64; accept only
		// values that round-trip cleanly to one of the defined
		// constants.
		if x != float64(int64(x)) {
			return SeverityInfo
		}
		return severityFromInt(int64(x))
	default:
		return SeverityInfo
	}
}

// severityFromKeyword maps a wire-format severity keyword to its
// constant. Unknown keywords yield SeverityInfo. Match is
// case-sensitive lowercase by design (see SeverityOf godoc).
func severityFromKeyword(s string) Severity {
	// Reject any non-lowercase form; emitters serialize lowercase
	// per the wire contract.
	if s != strings.ToLower(s) {
		return SeverityInfo
	}
	switch s {
	case "debug":
		return SeverityDebug
	case "info":
		return SeverityInfo
	case "warn":
		return SeverityWarn
	case "error":
		return SeverityError
	case "critical":
		return SeverityCritical
	default:
		return SeverityInfo
	}
}

// severityFromInt maps an integer value to a Severity constant.
// Out-of-range values yield SeverityInfo.
func severityFromInt(n int64) Severity {
	if n < int64(SeverityDebug) || n > int64(SeverityCritical) {
		return SeverityInfo
	}
	return Severity(n)
}
