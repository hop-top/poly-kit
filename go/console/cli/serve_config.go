package cli

import (
	"time"

	"github.com/spf13/viper"
	"hop.top/kit/go/console/serve"
)

// Config keys for the serve hierarchy (contract §"Configuration
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
)

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

		if v == nil || !v.IsSet(key) {
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
