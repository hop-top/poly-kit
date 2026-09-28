package cli

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/spf13/viper"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/transport/api"
)

// Middleware block keys for the body limit. The block is
// services.<svc>.body_limit, with shared defaults under
// services.all.body_limit.
const (
	bodyLimitBlock    = "body_limit"
	bodyLimitMaxBytes = "max_bytes"
	bodyLimitEnabled  = "enabled"
)

// serviceMaxBodyBytes resolves the request body cap for service name,
// per key: services.<name>.body_limit.* first, then
// services.all.body_limit.*, then fallback (the service's Go option),
// then api.DefaultMaxBodyBytes. The result is the cap, or a negative
// value for no cap (enabled: false, or a negative Go option).
//
// max_bytes is a whole number of bytes, zero meaning the default;
// enabled is a boolean. Anything else is an error naming the key,
// which the service reports from Validate rather than serving with a
// limit nobody wrote.
func serviceMaxBodyBytes(v *viper.Viper, name string, fallback int64) (int64, error) {
	cfg := svcconfig.New(v)
	if err := cfg.ValidateBlock(bodyLimitBlock, name, svcconfig.Shared); err != nil {
		return 0, err
	}
	limit := fallback
	if raw, key, ok := cfg.Lookup(name, bodyLimitBlock, bodyLimitMaxBytes); ok {
		n, err := wholeBytes(raw)
		if err == nil && n < 0 {
			err = fmt.Errorf("%d is negative; set %s: false to disable the limit", n, bodyLimitEnabled)
		}
		if err != nil {
			return 0, fmt.Errorf("%s: %w", key, err)
		}
		limit = n
	}
	if raw, key, ok := cfg.Lookup(name, bodyLimitBlock, bodyLimitEnabled); ok {
		on, err := boolValue(raw)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", key, err)
		}
		if !on {
			return -1, nil
		}
		if limit < 0 {
			// Enabled by configuration: a Go option that disabled
			// the limit yields to the default rather than winning.
			limit = 0
		}
	}
	return api.MaxBodyBytesOrDefault(limit), nil
}

// boolValue reads a boolean config value: a bool, or the string an
// environment variable carries.
func boolValue(raw any) (bool, error) {
	switch x := raw.(type) {
	case bool:
		return x, nil
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(x))
		if err != nil {
			return false, fmt.Errorf("%q is not a boolean", x)
		}
		return b, nil
	default:
		return false, fmt.Errorf("%v is not a boolean", raw)
	}
}

// wholeBytes reads a byte count from a config value: an integer, or a
// string holding one. Units are not accepted; a byte count is exact.
func wholeBytes(raw any) (int64, error) {
	switch x := raw.(type) {
	case int:
		return int64(x), nil
	case int64:
		return x, nil
	case int32:
		return int64(x), nil
	case uint64:
		if x > math.MaxInt64 {
			return 0, fmt.Errorf("%d is out of range", x)
		}
		return int64(x), nil
	case float64:
		if x != math.Trunc(x) || x > math.MaxInt64 || x < math.MinInt64 {
			return 0, fmt.Errorf("%v is not a whole number of bytes", x)
		}
		return int64(x), nil
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%q is not a whole number of bytes", x)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("%v is not a whole number of bytes", raw)
	}
}

// maxBodyBytes is the api service's resolved body cap.
func (a *apiService) maxBodyBytes() (int64, error) { return a.plane().maxBodyBytes() }

// maxBodyBytes is the listener's resolved body cap.
func (p httpPlane) maxBodyBytes() (int64, error) {
	return serviceMaxBodyBytes(p.viper(), p.l.Service, p.l.MaxBodyBytes)
}

// bodyLimit returns the listener's body-cap middleware, HTTP-plane
// slot 10, reporting each refusal to the listener's hook (the api
// service audits it into the bridge's sinks, as the auth middleware
// does) and writing it in the listener's protocol. The cap was
// validated in Validate; an error here falls back to the default
// rather than serving uncapped.
func (p httpPlane) bodyLimit() api.Middleware {
	limit, err := p.maxBodyBytes()
	if err != nil {
		limit = api.DefaultMaxBodyBytes
	}
	var opts []api.BodyLimitOption
	if p.l.OnBodyTooLarge != nil {
		opts = append(opts, api.OnBodyTooLarge(p.l.OnBodyTooLarge))
	}
	if p.l.Refuse != nil {
		opts = append(opts, api.BodyTooLargeRefusal(p.l.Refuse))
	}
	return api.BodyLimit(limit, opts...)
}
