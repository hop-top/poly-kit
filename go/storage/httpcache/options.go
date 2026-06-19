package httpcache

import "time"

// Option configures a Transport at construction time. Follows the kit
// functional-options idiom (see core/breaker/options.go).
type Option func(*config)

type config struct {
	ttl    time.Duration
	prefix string
}

// defaultPrefix namespaces cache keys so multiple callers can share one
// kv backend without collision. It is part of the cross-language
// contract: the TS and Python ports use the same default.
const defaultPrefix = "httpcache:"

func newConfig(opts ...Option) config {
	c := config{ttl: 24 * time.Hour, prefix: defaultPrefix}
	for _, o := range opts {
		o(&c)
	}
	if c.prefix == "" {
		c.prefix = defaultPrefix
	}
	return c
}

// WithTTL sets how long a stored response is served before it is
// treated as a miss. A non-positive duration falls back to the store's
// own expiry behavior via PutWithTTL. Default 24h.
func WithTTL(d time.Duration) Option { return func(c *config) { c.ttl = d } }

// WithPrefix overrides the cache-key namespace. Empty restores the
// default ("httpcache:").
func WithPrefix(p string) Option { return func(c *config) { c.prefix = p } }
