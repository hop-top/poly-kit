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
