package observability

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cast"
	"github.com/spf13/viper"
)

// The configuration blocks this package owns. Each lives in a
// service's own block, services.<svc>.tracing and
// services.<svc>.metrics, and may be set once for every service under
// services.all.
const (
	BlockTracing = "tracing"
	BlockMetrics = "metrics"
)

// Exporter names accepted by the exporter key.
const (
	// ExporterOTLP pushes OTLP over HTTP (protobuf) to a collector.
	ExporterOTLP = "otlp"
	// ExporterStdout writes spans or metrics as JSON lines to the
	// process's standard output (or the writer given by [WithOutput]).
	ExporterStdout = "stdout"
)

// DefaultEndpoint is where the OTLP exporters send when neither the
// configuration nor the standard OTEL_EXPORTER_OTLP_* environment
// names an endpoint: a collector on loopback, so enabling export never
// reaches beyond the host by default.
const DefaultEndpoint = "http://127.0.0.1:4318"

// DefaultMetricsInterval is how often metrics are exported.
const DefaultMetricsInterval = 60 * time.Second

// servicesKey and allServices spell the configuration path segments.
const (
	servicesKey = "services"
	allServices = "all"
)

// Keys every block accepts, and the ones one block adds. An unknown key
// inside a block is a configuration error: a misspelled key that
// silently leaves export off is the failure this prevents.
var (
	commonKeys  = []string{"enabled", "exporter", "endpoint", "headers"}
	tracingKeys = append(slices.Clone(commonKeys), "sample_ratio")
	metricsKeys = append(slices.Clone(commonKeys), "interval")
)

// Signal is one block's resolved configuration.
type Signal struct {
	// Enabled turns export on. Default false.
	Enabled bool
	// Exporter is ExporterOTLP (default) or ExporterStdout.
	Exporter string
	// Endpoint is the OTLP base URL (scheme, host, port). Empty
	// defers to OTEL_EXPORTER_OTLP_* when set, else DefaultEndpoint.
	Endpoint string
	// Headers are sent with every OTLP export request (a collector
	// token, a tenant header).
	Headers map[string]string
	// SampleRatio is the fraction of new traces recorded, applied
	// only to traces with no sampled parent (tracing only). Default 1.
	SampleRatio float64
	// Interval is the metrics export period (metrics only). Default
	// DefaultMetricsInterval.
	Interval time.Duration
}

// Config is one service's tracing and metrics configuration.
type Config struct {
	Tracing Signal
	Metrics Signal
}

// Enabled reports whether either signal is on.
func (c Config) Enabled() bool { return c.Tracing.Enabled || c.Metrics.Enabled }

// Resolve reads service's tracing and metrics blocks from v. Each key
// resolves on its own, most specific first: services.<service>.<block>.<key>,
// then services.all.<block>.<key>, then the default. A nil v resolves
// every default, which is both signals off.
func Resolve(v *viper.Viper, service string) (Config, error) {
	var cfg Config
	var err error
	if cfg.Tracing, err = resolveSignal(v, service, BlockTracing); err != nil {
		return Config{}, err
	}
	if cfg.Metrics, err = resolveSignal(v, service, BlockMetrics); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// lookup is the one place a key is resolved: the service's own key,
// then the services.all key. It returns the full key it found, for
// error messages, and ok=false when neither is set.
//
// It is deliberately small so a resolver shared by every middleware
// block can replace it without touching the callers.
func lookup(v *viper.Viper, service, block, key string) (value any, from string, ok bool) {
	if v == nil {
		return nil, "", false
	}
	for _, svc := range []string{service, allServices} {
		if svc == "" {
			continue
		}
		k := strings.Join([]string{servicesKey, svc, block, key}, ".")
		if v.IsSet(k) {
			return v.Get(k), k, true
		}
	}
	return nil, "", false
}

// resolveSignal resolves one block for service, key by key.
func resolveSignal(v *viper.Viper, service, block string) (Signal, error) {
	sig := Signal{Exporter: ExporterOTLP, SampleRatio: 1, Interval: DefaultMetricsInterval}
	var errs []error
	get := func(key string, set func(val any) error) {
		val, from, ok := lookup(v, service, block, key)
		if !ok {
			return
		}
		if err := set(val); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", from, err))
		}
	}

	get("enabled", func(val any) error { return decode(val, &sig.Enabled) })
	get("exporter", func(val any) error {
		var s string
		if err := decode(val, &s); err != nil {
			return err
		}
		switch s = strings.ToLower(strings.TrimSpace(s)); s {
		case ExporterOTLP, ExporterStdout:
			sig.Exporter = s
			return nil
		}
		return fmt.Errorf("unknown exporter %q (want %q or %q)", s, ExporterOTLP, ExporterStdout)
	})
	get("endpoint", func(val any) error { return decode(val, &sig.Endpoint) })
	get("headers", func(val any) error { return decode(val, &sig.Headers) })
	switch block {
	case BlockTracing:
		get("sample_ratio", func(val any) error {
			if err := decode(val, &sig.SampleRatio); err != nil {
				return err
			}
			if sig.SampleRatio < 0 || sig.SampleRatio > 1 {
				return fmt.Errorf("%v is outside [0, 1]", sig.SampleRatio)
			}
			return nil
		})
	case BlockMetrics:
		get("interval", func(val any) error {
			if err := decode(val, &sig.Interval); err != nil {
				return err
			}
			if sig.Interval <= 0 {
				return errors.New("must be positive")
			}
			return nil
		})
	}
	return sig, errors.Join(errs...)
}

// Validate refuses an unknown key inside any tracing or metrics block
// in v, under every service and services.all, so a misspelling fails
// at start instead of silently leaving export off.
func Validate(v *viper.Viper) error {
	if v == nil {
		return nil
	}
	services := v.GetStringMap(servicesKey)
	var errs []error
	for _, svc := range slices.Sorted(maps.Keys(services)) {
		for block, allowed := range map[string][]string{BlockTracing: tracingKeys, BlockMetrics: metricsKeys} {
			prefix := strings.Join([]string{servicesKey, svc, block}, ".")
			if !v.IsSet(prefix) {
				continue
			}
			if _, isBlock := v.Get(prefix).(map[string]any); !isBlock {
				errs = append(errs, fmt.Errorf("%s: must be a block of keys", prefix))
				continue
			}
			sub := v.GetStringMap(prefix)
			for _, key := range slices.Sorted(maps.Keys(sub)) {
				if !slices.Contains(allowed, key) {
					errs = append(errs, fmt.Errorf("%s.%s: unknown key (known: %s)",
						prefix, key, strings.Join(allowed, ", ")))
				}
			}
		}
	}
	return errors.Join(errs...)
}

// decode converts a configuration value into dst with viper's own
// casting library, so "true", "0.5" and "30s" from the environment
// read the same as native YAML values.
func decode(val any, dst any) error {
	var err error
	switch d := dst.(type) {
	case *bool:
		*d, err = cast.ToBoolE(val)
	case *string:
		*d, err = cast.ToStringE(val)
	case *float64:
		*d, err = cast.ToFloat64E(val)
	case *time.Duration:
		*d, err = cast.ToDurationE(val)
	case *map[string]string:
		*d, err = cast.ToStringMapStringE(val)
	default:
		err = fmt.Errorf("unsupported destination %T", dst)
	}
	return err
}
