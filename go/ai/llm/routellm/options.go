package routellm

import (
	charmlog "charm.land/log/v2"
	"github.com/spf13/viper"

	kitlog "hop.top/kit/go/console/log"
)

// Option configures a ConfigWatcher at construction time. Apply via
// NewConfigWatcher(path, onChange, opts...).
type Option func(*config)

// config is the internal accumulator filled by Option callbacks.
//
// The exported routellm.Logger interface (LogRoute / LogEva) is a
// distinct concern — it captures structured router decisions for
// downstream sinks. This config.logger is the kit/log structured
// logger used for watcher diagnostics (stat / parse failures), so
// adopter --quiet / --no-color flow through.
type config struct {
	logger *charmlog.Logger
}

// defaultConfig returns a config with kit/log-resolved defaults.
//
// The logger is built from viper.GetViper(), so adopters who configure
// the global viper (e.g. via kit/cli) automatically inherit "quiet" and
// "no-color" semantics. Callers that need a different logger pass
// WithLogger.
func defaultConfig() *config {
	return &config{
		logger: kitlog.New(viper.GetViper()),
	}
}

// WithLogger overrides the structured logger used for watcher
// diagnostics (stat-failed and parse-failed warnings emitted from the
// poll loop).
//
// When unset, NewConfigWatcher defaults to kitlog.New(viper.GetViper())
// so adopter configuration flows through. Tests that need to capture
// log output should pass a logger built with SetOutput pointed at a
// bytes.Buffer.
func WithLogger(l *charmlog.Logger) Option {
	return func(c *config) {
		if l != nil {
			c.logger = l
		}
	}
}
