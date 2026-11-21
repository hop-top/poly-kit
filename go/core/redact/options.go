package redact

import (
	charmlog "charm.land/log/v2"
	"github.com/spf13/viper"

	kitlog "hop.top/kit/go/console/log"
)

// Option configures a Redactor at construction time. Options compose
// via New(opts ...Option); each callback mutates the Redactor before
// rules are added.
type Option func(*Redactor)

// WithLogger overrides the structured logger the redactor uses for
// internal warnings (e.g. when a Custom replacement formatter panics
// and the engine falls back to Mask).
//
// When unset, New defaults to kitlog.New(viper.GetViper()) so adopter
// configuration (--quiet, --no-color, verbosity) flows through. Tests
// that need to capture log output should pass a logger built with
// SetOutput pointed at a bytes.Buffer.
func WithLogger(l *charmlog.Logger) Option {
	return func(r *Redactor) {
		if l != nil {
			r.logger = l
		}
	}
}

// defaultLogger returns the viper-aware kit/log logger. Resolved at
// construction time so each Redactor captures the viper state then in
// effect.
func defaultLogger() *charmlog.Logger {
	return kitlog.New(viper.GetViper())
}

// WithValidator registers fn as an extra check for the rule with the
// exact id ruleID: a match of that rule is redacted only when fn
// returns true for the matched text (the secret span, as Match.Original
// reports it). It lets a caller attach a checksum such as ValidateIBAN
// to a shape-only regex.
//
// Semantics:
//
//   - fn runs in addition to the checks kit already applies to a rule
//     (token boundary, Luhn, SSN and ITIN structure); it never replaces
//     them, so registering for a kit rule id only narrows what is
//     redacted. Several validators for one id must all accept.
//   - The id is compared exactly. A Presidio rule renamed at load time
//     (suffix "-pii") is registered under its suffixed id.
//   - fn fails closed: a panic counts as "valid", so the match is still
//     redacted. fn must not retain or log its argument, which is the
//     matched secret.
//   - Validators are fixed at construction: there is no runtime
//     registration, because the table is read on every Apply and Scan
//     and AddRule is already documented as unsafe to call alongside
//     them. After New returns the table is read-only, so a Redactor
//     stays safe for concurrent Apply, Scan and Spans, and fn must be
//     safe for concurrent calls.
//   - New panics on an empty ruleID or a nil fn: a missing check would
//     silently redact more or less than the caller meant.
//
// NewEmbedded and NewFromPolicy forward their options, so
// NewEmbedded(WithValidator("iban", ValidateIBAN)) works. Only Default()
// takes no options and carries no validators.
func WithValidator(ruleID string, fn func(match string) bool) Option {
	if ruleID == "" || fn == nil {
		panic("redact: WithValidator requires a non-empty rule id and a non-nil func")
	}
	return func(r *Redactor) {
		if r.validators == nil {
			r.validators = make(map[string][]func(string) bool)
		}
		r.validators[ruleID] = append(r.validators[ruleID], fn)
	}
}
