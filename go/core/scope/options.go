package scope

import (
	"charm.land/log/v2"
	"github.com/spf13/viper"

	kitlog "hop.top/kit/go/console/log"
)

// Option configures a Policy at construction time. Apply via New(opts...).
type Option func(*config)

// config is the internal accumulator filled by Option callbacks.
type config struct {
	logger *log.Logger
}

// defaultConfig returns a config with kit/log-resolved defaults.
//
// The logger is built from viper.GetViper(), so adopters who configure the
// global viper (e.g. via kit/cli) automatically inherit "quiet" and
// "no-color" semantics. Callers that need a different logger pass
// WithLogger.
func defaultConfig() *config {
	return &config{
		logger: kitlog.New(viper.GetViper()),
	}
}

// WithLogger overrides the structured logger used for warn-mode and
// stat-error messages. When unset, the policy uses kitlog.New(viper.GetViper()).
func WithLogger(l *log.Logger) Option {
	return func(c *config) {
		if l != nil {
			c.logger = l
		}
	}
}
