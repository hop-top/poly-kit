// telemetry.go wires the optional kit-telemetry pipeline into the
// example. The wiring is gated behind the CMDSURFACE_DEMO_TELEMETRY=1
// environment variable so the default `go run ./examples/cmdsurface`
// experience stays inert (no consent prompt, no extra bus, no extra
// goroutines).
//
// When opted in:
//
//   - A dedicated kit bus.Bus is constructed for telemetry. The
//     example's own `exampleBus` satisfies cmdsurface.Subscriber +
//     api.EventPublisher, not bus.Bus, so we cannot reuse it for the
//     telemetry emitter without crossing two unrelated contracts.
//     The two buses live side by side; that mirrors how a real adopter
//     would wire telemetry separately from their app's domain pub/sub.
//   - The consent FileStore is installed from XDG. Failure to install
//     leaves the package-level default-deny in place, which means
//     telemetry stays inert until the operator runs `kit telemetry enable`.
//   - The sink defaults to telemetry.ModeAnon. This lets the emitter
//     construct without a redactor; flipping to Full requires wiring
//     WithRedactor — out of scope for the demo.
//
// Path chosen for T-0681: we use the TelemetryOption constructor
// directly. The cmdsurface.Config telemetry block (T-0676) is not
// consulted here. Switching to the config path is a single-line swap
// the moment T-0676 lands a public ConfigureTelemetry helper.
package main

import (
	"context"
	"log/slog"

	"hop.top/kit/go/core/consent"
	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/telemetry"
	"hop.top/kit/go/transport/cmdsurface"
)

// demoVersion is the version stamped onto every emitted telemetry
// event. A real adopter injects this at build time via -ldflags; the
// example uses a literal so `go run` produces a stable identifier.
const demoVersion = "0.0.0-demo"

// demoAppPrefix is the app prefix the telemetry emitter uses to derive
// the wire topic ("<prefix>.telemetry.event.recorded") and the
// app-specific mode env var ("<PREFIX>_TELEMETRY_MODE"). Adopters set
// this to their own app name.
const demoAppPrefix = "cmdsurface-demo"

// telemetryResources bundles the runtime objects the telemetry wiring
// creates so BuildExample can plumb cleanup through exampleApp.
//
// Bus is the dedicated kit bus.Bus for telemetry traffic. Sink is the
// TelemetrySink registered on the cmdsurface SinkSet. Either may be
// nil when telemetry is disabled (the env gate did not opt in or
// construction failed soft).
type telemetryResources struct {
	Bus  bus.Bus
	Sink *cmdsurface.TelemetrySink
}

// Close releases the telemetry resources. Idempotent; nil-safe.
//
// Ordering: close the sink first (drains in-flight events through the
// emitter) and then the bus (so the drain has a publish target until
// the very last event lands). The Close context bounds the wait; the
// caller passes a non-canceled context to allow the drain to finish.
func (r *telemetryResources) Close(ctx context.Context) {
	if r == nil {
		return
	}
	if r.Sink != nil {
		_ = r.Sink.Close(ctx)
	}
	if r.Bus != nil {
		_ = r.Bus.Close(ctx)
	}
}

// maybeBuildTelemetry constructs the optional TelemetrySink + supporting
// bus + emitter when telemetryEnabled is true. Returns a nil sink and
// nil error when telemetry is disabled — the caller treats both as "no
// sink to append".
//
// Construction failures are logged and downgraded to nil; the demo
// must keep working even when the operator has a broken XDG dir or no
// consent file. The whole point of this wiring is to show the
// inert-by-default property.
func maybeBuildTelemetry(logger *slog.Logger, telemetryEnabled bool) (*telemetryResources, error) {
	if !telemetryEnabled {
		return nil, nil
	}

	// Register the app prefix so <PREFIX>_TELEMETRY_MODE works. The
	// emitter's wire topic still uses the prefix we pass via
	// WithTopicPrefix below — SetAppPrefix only affects mode env-var
	// resolution.
	telemetry.SetAppPrefix(demoAppPrefix)

	// Install consent. Failure is non-fatal: the package-level
	// default-deny hook stays installed and telemetry remains inert
	// until the operator persists a consent decision via
	// `kit telemetry enable`.
	if _, err := consent.Install(); err != nil {
		logger.Warn("telemetry: consent install failed; telemetry will stay inert",
			"err", err)
	}

	// Dedicated bus for telemetry traffic. The demo's exampleBus
	// satisfies cmdsurface.Subscriber but not bus.Bus, so we cannot
	// reuse it. Real adopters either share the kit bus across both or
	// keep them separate as we do here.
	telBus := bus.New()

	emitter, err := telemetry.New(
		telemetry.WithBus(telBus),
		telemetry.WithTopicPrefix(demoAppPrefix+".telemetry.event"),
		telemetry.WithKitVersion(demoVersion),
		// No WithRedactor: the demo defaults to ModeAnon, so the
		// emitter constructor does not require one. Flipping the
		// global mode to Full at runtime would surface as a
		// configuration error at telemetry.New() time — by design,
		// per ADR-0035.
	)
	if err != nil {
		_ = telBus.Close(context.Background())
		logger.Warn("telemetry: emitter construction failed; telemetry disabled",
			"err", err)
		return nil, nil
	}

	sink, err := cmdsurface.NewTelemetrySink(
		cmdsurface.WithEmitter(emitter),
		cmdsurface.WithMode(telemetry.ModeAnon),
		cmdsurface.WithKitVersion(demoVersion),
	)
	if err != nil {
		_ = telBus.Close(context.Background())
		logger.Warn("telemetry: sink construction failed; telemetry disabled",
			"err", err)
		return nil, nil
	}

	logger.Info("telemetry: enabled",
		"mode", "anon",
		"topic", demoAppPrefix+".telemetry.event.recorded",
		"hint", "run `kit telemetry enable` once per machine to grant consent")

	return &telemetryResources{Bus: telBus, Sink: sink}, nil
}
