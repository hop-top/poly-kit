package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/console/serve"
)

// Config keys for the serve hierarchy (serve-lifecycle.md §"Configuration
// surface"). Resolution follows the standard kit precedence — flag,
// env, config file, default — because these are read through the
// Root's viper rather than from a file directly.
const (
	serveKeyPrefix          = "services."
	serveKeyFailurePolicy   = "services.failure_policy"
	serveKeyShutdownTimeout = "services.shutdown_timeout"

	serveSubkeyEnabled      = ".enabled"
	serveSubkeyReadyTimeout = ".ready_timeout"
	serveSubkeyStopTimeout  = ".stop_timeout"
	serveSubkeyOptional     = ".optional"
)

// applyServiceOptional resolves services.<name>.optional for each of
// names into configs, creating the entry of a service that has no
// block of its own: the selector form runs a service whatever its
// configuration, and its registration's default still applies. Call it
// after [serve.Resolve], whose supervisor form reads an entry as "is
// configured"; names is normally the resolved selection.
func applyServiceOptional(v *viper.Viper, reg *serve.Registry, configs map[string]serve.Config, names []string) error {
	if reg == nil {
		return nil
	}
	for _, name := range names {
		svc, ok := reg.Lookup(name)
		if !ok {
			continue
		}
		optional, err := serviceOptional(v, svc)
		if err != nil {
			return err
		}
		cfg := configs[name]
		cfg.Optional = optional
		configs[name] = cfg
	}
	return nil
}

// validateServiceOptional refuses a services.<name>.optional, for any
// registered service, that is not a boolean: a typo there would
// otherwise read as "required" without a word.
func validateServiceOptional(v *viper.Viper, reg *serve.Registry) error {
	if v == nil || reg == nil {
		return nil
	}
	for _, svc := range reg.List() {
		if _, err := serviceOptional(v, svc); err != nil {
			return err
		}
	}
	return nil
}

// serviceOptional is services.<name>.optional when any source sets
// it, else the registration's own default ([serve.Optional]), else
// false. The key wins in either direction: an adopter marks its
// auxiliary service optional by default, and an operator who needs it
// can make it required again, or the reverse
// (serve-lifecycle.md §"Optional services"). A value that is not a
// boolean — true or false, or a string strconv.ParseBool reads, as an
// environment variable carries one — is an error naming the key.
func serviceOptional(v *viper.Viper, svc serve.Service) (bool, error) {
	if v != nil {
		if key := serveKeyPrefix + svc.Name() + serveSubkeyOptional; v.IsSet(key) {
			switch raw := v.Get(key).(type) {
			case bool:
				return raw, nil
			case string:
				b, err := strconv.ParseBool(strings.TrimSpace(raw))
				if err == nil {
					return b, nil
				}
			}
			return false, fmt.Errorf("%s: %v is not a boolean; use true or false", key, quoteValue(v.Get(key)))
		}
	}
	o, ok := svc.(serve.Optional)
	return ok && o.Optional(), nil
}

// quoteValue renders a config value for a refusal: strings quoted,
// anything else as written.
func quoteValue(v any) string {
	if s, ok := v.(string); ok {
		return strconv.Quote(s)
	}
	return fmt.Sprintf("%v", v)
}

// serveConfigs resolves the services.<name> block for every registered
// service.
//
// A service with no block at all is not configured, and the supervisor
// form skips it; a service with any key under its block is configured,
// and its enablement is whatever services.<name>.enabled resolves to
// (default false). Enablement defaulting to false is deliberate: a
// service that starts listening because a dependency upgrade added it
// to the registry is an unrequested open port.
//
// The enable/disable flag overrides apply on top, and only under the
// supervisor form — the selector form's override rule makes them
// redundant there.
func serveConfigs(v *viper.Viper, names []string, enable, disable []string) map[string]serve.Config {
	forced := make(map[string]bool, len(enable)+len(disable))
	for _, n := range disable {
		forced[n] = false
	}
	// Enable wins over disable when an operator passes both for the
	// same name: the affirmative act is the more specific one.
	for _, n := range enable {
		forced[n] = true
	}

	out := make(map[string]serve.Config, len(names))
	for _, name := range names {
		key := serveKeyPrefix + name
		want, isForced := forced[name]

		if !svcconfig.New(v).IsConfigured(name) {
			// An unconfigured service becomes configured the moment
			// an operator names it in --enable: the flag is the
			// aggregate equivalent of the selector's override.
			if isForced && want {
				out[name] = serve.Config{Enabled: true}
			}
			continue
		}

		cfg := serve.Config{
			Enabled:      v.GetBool(key + serveSubkeyEnabled),
			ReadyTimeout: v.GetDuration(key + serveSubkeyReadyTimeout),
			StopTimeout:  v.GetDuration(key + serveSubkeyStopTimeout),
		}
		if isForced {
			cfg.Enabled = want
		}
		out[name] = cfg
	}
	return out
}

// serveSupervisorConfig resolves the supervisor-scoped half of the
// services block. Unset or unparseable values fall back to the
// documented defaults rather than refusing to start, except
// failure_policy: an unrecognized policy is a configuration error the
// caller surfaces, because silently running fail-fast when the
// operator asked for isolate is the kind of surprise that costs a
// production incident.
func serveSupervisorConfig(v *viper.Viper) (serve.SupervisorConfig, string) {
	cfg := serve.SupervisorConfig{
		FailurePolicy:   serve.DefaultFailurePolicy,
		ShutdownTimeout: serve.DefaultShutdownTimeout,
	}
	if v == nil {
		return cfg, ""
	}

	if raw := v.GetString(serveKeyFailurePolicy); raw != "" {
		p := serve.FailurePolicy(raw)
		if !p.IsValid() {
			return cfg, raw
		}
		cfg.FailurePolicy = p
	}
	if d := v.GetDuration(serveKeyShutdownTimeout); d > 0 {
		cfg.ShutdownTimeout = d
	}
	return cfg, ""
}

// serveTimeoutOverrides applies the --ready-timeout / --stop-timeout
// flags across every resolved service. The flags map onto the
// per-service keys, so a flag set on the supervisor form applies the
// same budget to every member of the set — which is what an operator
// tuning a whole process, rather than one service, is asking for.
func serveTimeoutOverrides(configs map[string]serve.Config, ready, stop time.Duration) {
	if ready <= 0 && stop <= 0 {
		return
	}
	for name, cfg := range configs {
		if ready > 0 {
			cfg.ReadyTimeout = ready
		}
		if stop > 0 {
			cfg.StopTimeout = stop
		}
		configs[name] = cfg
	}
}
