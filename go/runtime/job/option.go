package job

import (
	"time"

	"hop.top/kit/go/runtime/domain"
)

// Config holds resolved configuration for job components.
type Config struct {
	Publisher domain.EventPublisher
	NowFunc   func() time.Time
}

// Option configures job package behavior.
type Option func(*Config)

// WithPublisher sets the event publisher for job lifecycle events.
func WithPublisher(p domain.EventPublisher) Option {
	return func(c *Config) { c.Publisher = p }
}

// WithNowFunc overrides the clock function (useful for testing).
func WithNowFunc(f func() time.Time) Option {
	return func(c *Config) { c.NowFunc = f }
}

// BuildConfig resolves options into a Config with defaults applied.
func BuildConfig(opts []Option) Config {
	cfg := Config{
		NowFunc: time.Now,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}
