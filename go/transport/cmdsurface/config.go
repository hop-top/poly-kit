package cmdsurface

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"hop.top/kit/go/runtime/telemetry"
)

// Config is the YAML-loadable shape that drives a Bridge. The
// foundation wave covers Surfaces (per-leaf enablement) and Policy
// (destructive default + DefaultEnabled). Surface-specific config
// blocks (webhook, bus, cron, sinks) are reserved on the struct
// but ignored by the loader until later waves add the surface
// implementations.
type Config struct {
	Surfaces SurfacesConfig `yaml:"surfaces"`
	Policy   PolicyConfig   `yaml:"policy"`
	// Telemetry is the optional kit-telemetry sink configuration.
	// Pointer type so absence (nil) is distinguishable from a
	// zero-valued enabled-false block. The bridge wiring (T-0677)
	// reads cfg.Telemetry != nil && cfg.Telemetry.Enabled to decide
	// whether to construct the TelemetrySink. See TelemetryConfig
	// godoc and the cmdsurf-telemetry track design note.
	Telemetry *TelemetryConfig `yaml:"telemetry,omitempty" json:"telemetry,omitempty"`

	// TelemetryEmitterProvider is invoked by FromConfig when
	// Telemetry.Enabled is true. Required when Telemetry.Enabled;
	// ignored otherwise. The returned emitter is owned by the bridge
	// (passed verbatim into the TelemetrySink via WithEmitter); the
	// bridge will Close the sink — not the emitter — on Bridge.Close.
	//
	// Adopters typically construct the emitter via telemetry.New(...)
	// with their own bus, redactor, and topic prefix. The provider
	// shape (factory func instead of *telemetry.Emitter directly)
	// keeps construction lazy: the emitter is only built when the
	// telemetry block resolves to enabled, so adopters can keep the
	// emitter-bus dependency out of disabled paths.
	//
	// Marshaling tags are "-": functions cannot round-trip through
	// YAML / JSON. The provider must come from in-Go code, not config
	// files.
	TelemetryEmitterProvider func() (*telemetry.Emitter, error) `yaml:"-" json:"-"`
}

// SurfacesConfig is the surfaces: block. Defaults is the
// fallback enablement set for leaves that have no per-command
// entry; Commands maps a path pattern (exact, "<prefix> *", or
// "*") to a per-command override.
type SurfacesConfig struct {
	Defaults []Surface                `yaml:"defaults"`
	Commands map[string]CommandConfig `yaml:"commands"`
}

// CommandConfig is the per-command override under
// surfaces.commands.<pattern>. The Enabled field drives the
// bridge's per-leaf surface set; the Webhook / Bus / Cron / Sinks
// blocks are the declarative counterpart to the Go-side mount
// inputs (WebhookMapping, BusBinding, CronSchedule, SinkSpec)
// documented in .tlc/tracks/cmdsurf/spec.md. The blocks are
// parsed by Load and surfaced unchanged; callers building Mount*
// inputs from YAML translate each block to the corresponding
// runtime struct.
type CommandConfig struct {
	// Enabled lists the surfaces this command is exposed on. When
	// empty the bridge falls back to SurfacesConfig.Defaults (or
	// the Policy default if Defaults is also empty).
	Enabled []Surface `yaml:"enabled"`

	// Webhook is the per-command webhook mapping (see WebhookConfig).
	Webhook WebhookConfig `yaml:"webhook,omitempty"`
	// Bus is the per-command bus binding (see BusConfig).
	Bus BusConfig `yaml:"bus,omitempty"`
	// Cron is the per-command cron schedule (see CronConfig).
	Cron CronConfig `yaml:"cron,omitempty"`
	// Sinks is the per-command outbound fan-out list (see SinkConfig).
	Sinks []SinkConfig `yaml:"sinks,omitempty"`
}

// PolicyConfig is the policy: block. The foundation honors
// DestructiveDefault; signed-URL and OAuth blocks are reserved
// for later waves.
type PolicyConfig struct {
	// DestructiveDefault is "deny_remote" (default) or "allow".
	// "deny_remote" enforces the DefaultPolicy() rule that
	// destructive leaves are unreachable on any non-CLI / non-Lib
	// surface unless explicitly opted-in via the per-command
	// enabled list. "allow" lifts the destructive ceiling so
	// every surface that lists a destructive leaf in its enabled
	// set may invoke it.
	DestructiveDefault string `yaml:"destructive_default"`
}

// Load parses YAML from r into a Config. Unknown keys are
// ignored to keep the foundation tolerant of forward-looking YAML
// (webhook/bus/cron blocks adopters write today and later waves
// consume).
func Load(r io.Reader) (Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(r)
	if err := dec.Decode(&cfg); err != nil {
		if err == io.EOF {
			return cfg, nil
		}
		return Config{}, fmt.Errorf("cmdsurface: parse config: %w", err)
	}
	return cfg, nil
}

// LoadFile is a convenience wrapper around Load for filesystem
// config paths.
func LoadFile(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("cmdsurface: open config %s: %w", path, err)
	}
	defer f.Close()
	return Load(f)
}

// FromConfig constructs a Bridge from a parsed Config. Option
// values (WithRunner, WithPolicy) layer on top of the config:
// they override anything the YAML set. The function applies
// surface enablement in two passes:
//
//  1. Replace each leaf's default Enabled set with cfg.Surfaces.Defaults
//     (when non-empty).
//  2. Walk cfg.Surfaces.Commands and apply per-pattern Enabled
//     lists, replacing whatever step 1 set.
func FromConfig(root *cobra.Command, cfg Config, opts ...Option) (*Bridge, error) {
	if root == nil {
		return nil, fmt.Errorf("cmdsurface: nil cobra root")
	}
	// Build the Policy from cfg.Policy first so the bridge's
	// default-enabled set picks it up at discovery time.
	policy := policyFromConfig(cfg.Policy)
	if len(cfg.Surfaces.Defaults) > 0 {
		policy.DefaultEnabled = append([]Surface(nil), cfg.Surfaces.Defaults...)
	}
	// User options take precedence: prepend policy so explicit
	// WithPolicy overrides it.
	finalOpts := append([]Option{WithPolicy(policy)}, opts...)

	b := New(root, finalOpts...)

	// Per-command overrides: replace the default enabled set on
	// matching leaves.
	for pattern, cc := range cfg.Surfaces.Commands {
		if len(cc.Enabled) == 0 {
			continue
		}
		if err := validateSurfaces(cc.Enabled); err != nil {
			return nil, fmt.Errorf("cmdsurface: config %q: %w", pattern, err)
		}
		b.applyCommandConfig(pattern, cc.Enabled)
	}

	// Telemetry sink wiring (T-0677). The kit-telemetry sink is the
	// first sink that FromConfig constructs on the bridge's behalf —
	// other sinks (bus/file/webhook/log) remain adopter-wired via the
	// sinkRunner pattern documented in README.md. Telemetry is the
	// exception because the kit-telemetry pipeline owns identity,
	// redaction, and consent, and adopters should not have to
	// re-implement that wiring per command.
	if cfg.Telemetry != nil && cfg.Telemetry.Enabled {
		cfg.Telemetry.ApplyDefaults()
		if cfg.TelemetryEmitterProvider == nil {
			return nil, errors.New(
				"cmdsurface: telemetry sink enabled in config but " +
					"TelemetryEmitterProvider not set on cmdsurface.Config",
			)
		}
		emitter, err := cfg.TelemetryEmitterProvider()
		if err != nil {
			return nil, fmt.Errorf("cmdsurface: telemetry emitter provider: %w", err)
		}
		mode, _ := telemetry.ParseMode(cfg.Telemetry.Mode)
		sink, err := NewTelemetrySink(
			WithEmitter(emitter),
			WithMode(mode),
			WithChannelCap(cfg.Telemetry.ChannelCap),
			WithMaxBytes(cfg.Telemetry.MaxBytes),
			WithKitVersion(cfg.Telemetry.KitVersion),
		)
		if err != nil {
			return nil, fmt.Errorf("cmdsurface: telemetry sink: %w", err)
		}
		b.appendSink(SinkSpec{
			Sink:    sink,
			OnOK:    true,
			OnError: true,
		})
	}
	return b, nil
}

// applyCommandConfig overrides the Enabled map of every leaf
// matching pattern with the exact set in surfaces. Existing
// entries on those leaves are cleared first so an enabled list of
// [cli, lib] truly disables every other surface.
func (b *Bridge) applyCommandConfig(pattern string, surfaces []Surface) {
	b.mu.Lock()
	defer b.mu.Unlock()
	set := make(map[Surface]bool, len(surfaces))
	for _, s := range surfaces {
		set[s] = true
	}
	for _, leaf := range b.leaves {
		if !matchPattern(pattern, leaf.Path) {
			continue
		}
		// Reset and reassign so the per-command list is authoritative.
		for k := range leaf.Enabled {
			delete(leaf.Enabled, k)
		}
		for s := range set {
			leaf.Enabled[s] = true
		}
	}
}

// policyFromConfig translates a PolicyConfig into a Policy.
// "allow" lifts the destructive ceiling for every surface;
// "deny_remote" (default) keeps the conservative behavior.
// Unknown values silently fall back to deny_remote — the loader
// validates allowed strings.
func policyFromConfig(pc PolicyConfig) Policy {
	p := DefaultPolicy()
	if pc.DestructiveDefault == "allow" {
		p.AllowDestructiveOn = AllSurfaces()
	}
	return p
}

// validateSurfaces returns an error if any surface in ss is not a
// recognized identifier.
func validateSurfaces(ss []Surface) error {
	for _, s := range ss {
		if !s.IsValid() {
			return fmt.Errorf("unknown surface %q", s)
		}
	}
	return nil
}
