package cli

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/viper"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// Middleware block keys for the body limit. The block is
// services.<svc>.body_limit, with shared defaults under
// services.all.body_limit.
const (
	bodyLimitBlock      = "body_limit"
	bodyLimitMaxBytes   = "max_bytes"
	bodyLimitEnabled    = "enabled"
	serveSharedServices = "all"
)

// serviceBlockKey looks one middleware key up for service svc,
// specificity before source: services.<svc>.<block>.<key> from any
// source (flag, env, file), then services.all.<block>.<key>. It
// returns the raw value and the key that supplied it.
//
// It is the only place the lookup order lives, so a shared
// services.all resolver can replace it without touching callers.
func serviceBlockKey(v *viper.Viper, svc, block, key string) (raw any, from string, ok bool) {
	if v == nil {
		return nil, "", false
	}
	for _, scope := range []string{svc, serveSharedServices} {
		k := serveKeyPrefix + scope + "." + block + "." + key
		if v.IsSet(k) {
			return v.Get(k), k, true
		}
	}
	return nil, "", false
}

// checkBlockKeys refuses a key inside services.<svc>.<block> or
// services.all.<block> that the block does not define: a misspelled
// key that silently leaves a limit at its default is the failure
// this prevents.
func checkBlockKeys(v *viper.Viper, svc, block string, known ...string) error {
	if v == nil {
		return nil
	}
	for _, scope := range []string{svc, serveSharedServices} {
		k := serveKeyPrefix + scope + "." + block
		m, isMap := v.Get(k).(map[string]any)
		if !isMap {
			continue
		}
		for name := range m {
			if !slices.Contains(known, name) {
				return fmt.Errorf("%s.%s: unknown key; %s takes %s",
					k, name, block, strings.Join(known, ", "))
			}
		}
	}
	return nil
}

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
	if err := checkBlockKeys(v, name, bodyLimitBlock, bodyLimitEnabled, bodyLimitMaxBytes); err != nil {
		return 0, err
	}
	limit := fallback
	if raw, key, ok := serviceBlockKey(v, name, bodyLimitBlock, bodyLimitMaxBytes); ok {
		n, err := wholeBytes(raw)
		if err == nil && n < 0 {
			err = fmt.Errorf("%d is negative; set %s: false to disable the limit", n, bodyLimitEnabled)
		}
		if err != nil {
			return 0, fmt.Errorf("%s: %w", key, err)
		}
		limit = n
	}
	if raw, key, ok := serviceBlockKey(v, name, bodyLimitBlock, bodyLimitEnabled); ok {
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
func (a *apiService) maxBodyBytes() (int64, error) {
	var v *viper.Viper
	if a.root != nil {
		v = a.root.Viper
	}
	return serviceMaxBodyBytes(v, APIServiceName, a.cfg.MaxBodyBytes)
}

// bodyLimit returns the api service's body-cap middleware, auditing
// each refusal into the bridge's sinks as the auth middleware does.
// The cap was validated in Validate; an error here falls back to the
// default rather than serving uncapped.
func (a *apiService) bodyLimit(bridge *cmdsurface.Bridge) api.Middleware {
	limit, err := a.maxBodyBytes()
	if err != nil {
		limit = api.DefaultMaxBodyBytes
	}
	return api.BodyLimit(limit, api.OnBodyTooLarge(cmdsurface.ProjectionBodyTooLarge(bridge)))
}
