package cli

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/spf13/viper"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/transport/cmdsurface"
)

// The rate_limit block bounds how fast one caller may invoke commands
// on a service: invocation-plane slot 7, after the permission gate.
// Each side-effect tier has its own token bucket per caller, the
// caller being its authenticated principal (with its tenant), else
// its client address, else the surface. A refused call is
// rate_limited: HTTP 429 with Retry-After, Connect ResourceExhausted,
// an MCP isError result, the socket's RATE_LIMITED; exit class 64.
//
//	services:
//	  all:
//	    rate_limit:
//	      enabled: true            # default: on beyond loopback, off on loopback
//	      read:
//	        per_minute: 600        # tokens refilled per minute
//	        burst: 60              # tokens held at most
//	      write:                   # write tiers and undeclared commands
//	        per_minute: 120
//	        burst: 20
//	      destructive:
//	        per_minute: 12
//	        burst: 3
//	  api:
//	    rate_limit:
//	      write:
//	        burst: 40              # api only; the other keys still come from all
//
// Every key resolves on its own: the service's key from any source,
// then the services.all key, then the tool's code default
// ([WithServeRateLimit]), then the kit default (the numbers above,
// cmdsurface.DefaultRateLimit). Buckets live in memory, per service.
const (
	rateLimitBlock     = "rate_limit"
	rateLimitEnabled   = "enabled"
	rateLimitPerMinute = "per_minute"
	rateLimitBurst     = "burst"
)

// rateLimitTiers are the tier sub-blocks of rate_limit.
var rateLimitTiers = []cmdsurface.RateTier{
	cmdsurface.RateTierRead, cmdsurface.RateTierWrite, cmdsurface.RateTierDestructive,
}

// rateLimitCode is the Root field type holding WithServeRateLimit's
// value.
type rateLimitCode = cmdsurface.RateLimit

// WithServeRateLimit sets the tool's code defaults for the rate_limit
// block of every kit-shipped service: each tier's per_minute and
// burst apply where no configuration source sets that key, and a zero
// field keeps the kit default. It does not switch the limit on:
// enablement stays with the enabled key and the service's exposure.
func WithServeRateLimit(cfg cmdsurface.RateLimit) func(*Root) {
	return func(r *Root) { r.serveRateLimitCode = cfg }
}

// serveRateLimit resolves svc's rate_limit block over no code
// defaults; see serveRateLimitOver.
func serveRateLimit(v *viper.Viper, svc string, loopback bool) (cmdsurface.RateLimit, bool, error) {
	return serveRateLimitOver(v, svc, loopback, cmdsurface.RateLimit{})
}

// serveRateLimitOver resolves svc's rate_limit block, code supplying
// each tier key no configuration source sets. on reports whether
// the gate is installed: the enabled key when set, else true beyond
// loopback and false on loopback. An unknown key, a value of the
// wrong type, or a count that is not a positive whole number is a
// configuration error naming the key, whether or not the gate is on.
func serveRateLimitOver(v *viper.Viper, svc string, loopback bool, code cmdsurface.RateLimit) (cfg cmdsurface.RateLimit, on bool, err error) {
	on = !loopback
	if v == nil {
		return code, on, nil
	}
	r := svcconfig.New(v)
	if err := r.ValidateBlock(rateLimitBlock, svc, svcconfig.Shared); err != nil {
		return cfg, false, err
	}
	if raw, key, ok := r.Lookup(svc, rateLimitBlock, rateLimitEnabled); ok {
		b, err := boolValue(raw)
		if err != nil {
			return cfg, false, fmt.Errorf("%s: %w", key, err)
		}
		on = b
	}
	for _, tier := range rateLimitTiers {
		block := rateLimitBlock + "." + string(tier)
		if err := r.ValidateBlock(block, svc, svcconfig.Shared); err != nil {
			return cfg, false, err
		}
		rule := tierRule(code, tier)
		for _, k := range []string{rateLimitPerMinute, rateLimitBurst} {
			raw, key, ok := r.Lookup(svc, block, k)
			if !ok {
				continue
			}
			n, err := positiveCount(raw)
			if err != nil {
				return cfg, false, fmt.Errorf("%s: %w", key, err)
			}
			if k == rateLimitPerMinute {
				rule.PerMinute = n
			} else {
				rule.Burst = n
			}
		}
		switch tier {
		case cmdsurface.RateTierRead:
			cfg.Read = rule
		case cmdsurface.RateTierWrite:
			cfg.Write = rule
		case cmdsurface.RateTierDestructive:
			cfg.Destructive = rule
		}
	}
	return cfg, on, nil
}

// tierRule is cfg's rule for tier, as set (zero fields unset).
func tierRule(cfg cmdsurface.RateLimit, tier cmdsurface.RateTier) cmdsurface.RateRule {
	switch tier {
	case cmdsurface.RateTierRead:
		return cfg.Read
	case cmdsurface.RateTierDestructive:
		return cfg.Destructive
	default:
		return cfg.Write
	}
}

// serveRateLimitOptions returns the bridge option installing svc's
// rate limit, or none when the limit is off. loopback is the service's
// exposure: the literal-host rule, with the socket service and stdio
// on the loopback side.
func (r *Root) serveRateLimitOptions(svc string, loopback bool) ([]cmdsurface.Option, error) {
	cfg, on, err := serveRateLimitOver(r.Viper, svc, loopback, r.serveRateLimitCode)
	if err != nil || !on {
		return nil, err
	}
	return []cmdsurface.Option{cmdsurface.WithRateLimit(cfg)}, nil
}

// positiveCount reads a count from a config value: an integer, or a
// string holding one, of at least 1. A limit of zero would refuse
// every call; switch the block off with enabled: false instead.
func positiveCount(raw any) (int, error) {
	n, err := wholeCount(raw)
	if err != nil {
		return 0, err
	}
	if n < 1 || n > math.MaxInt32 {
		return 0, fmt.Errorf("%d is out of range; want 1 to %d (set %s: false to lift the limit)",
			n, math.MaxInt32, rateLimitEnabled)
	}
	return int(n), nil
}

// wholeCount reads a whole number from a config value: an integer, or
// a string holding one. The caller checks its range.
func wholeCount(raw any) (int64, error) {
	var n int64
	switch x := raw.(type) {
	case int:
		n = int64(x)
	case int64:
		n = x
	case int32:
		n = int64(x)
	case uint64:
		if x > math.MaxInt32 {
			return 0, fmt.Errorf("%d is out of range", x)
		}
		n = int64(x)
	case float64:
		if x != math.Trunc(x) {
			return 0, fmt.Errorf("%v is not a whole number", x)
		}
		n = int64(x)
	case string:
		p, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%q is not a whole number", x)
		}
		n = p
	default:
		return 0, fmt.Errorf("%v is not a whole number", raw)
	}
	return n, nil
}
