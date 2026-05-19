// telemetry.go wires the optional kit-telemetry pipeline into the
// FaaS demos (Lambda + Cloud Run). Both binaries call BuildBridge to
// get the cobra tree and bridge; BuildTelemetrySink is a parallel
// helper they can layer on top to attach the telemetry pipeline via
// the cmdsurface Runner sink chain.
//
// Wiring is gated by the CMDSURFACE_DEMO_TELEMETRY environment
// variable so the default Lambda / Cloud Run cold start pays nothing
// for telemetry until the operator opts in by setting it to "1" in
// the function's environment configuration.
package shared

import (
	"context"
	"log/slog"
	"os"

	"hop.top/kit/go/core/consent"
	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/telemetry"
	"hop.top/kit/go/transport/cmdsurface"
)

// TelemetryEnvVar gates the optional telemetry wiring. Exported so the
// e2e tests can refer to the same constant rather than literalising
// the env-var name in two places.
const TelemetryEnvVar = "CMDSURFACE_DEMO_TELEMETRY"

// demoVersion is the stamp applied to every emitted Event.KitVersion.
// FaaS deploys typically inject a real value via -ldflags during the
// `go build` step; the literal here keeps `go run ./examples/...` and
// the e2e tests producing a stable identifier.
const demoVersion = "0.0.0-demo"

// demoAppPrefix is the app prefix the FaaS demos share. It must match
// the topic the bus inspector subscribes to (kit telemetry inspect
// uses the configured prefix).
const demoAppPrefix = "cmdsurface-faas-demo"

// TelemetryResources bundles the runtime objects the wiring creates.
// Close cleans up the sink first (drains in-flight events) and the
// dedicated bus second. Either field may be nil when telemetry was
// opted out or construction failed soft.
type TelemetryResources struct {
	Bus  bus.Bus
	Sink *cmdsurface.TelemetrySink
}

// Close releases the telemetry resources. Idempotent; nil-safe. ctx
// bounds the drain wait.
func (r *TelemetryResources) Close(ctx context.Context) {
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

// MaybeBuildTelemetry returns a populated TelemetryResources when
// CMDSURFACE_DEMO_TELEMETRY=1, or (nil, nil) otherwise. Construction
// failures are logged at warn and downgraded to nil — the FaaS demos
// must keep handling events even when telemetry wiring breaks (the
// inert-by-default property).
//
// The caller appends Sink to its cmdsurface SinkSet (Cloud Run via
// the same sinkRunner pattern as `examples/cmdsurface`; Lambda by
// composing the handler chain). The example FaaS legs do not yet
// wire a sink fan-out themselves; the helper exists so they can adopt
// it consistently when that wiring lands.
func MaybeBuildTelemetry(logger *slog.Logger) (*TelemetryResources, error) {
	if os.Getenv(TelemetryEnvVar) != "1" {
		return nil, nil
	}

	telemetry.SetAppPrefix(demoAppPrefix)

	if _, err := consent.Install(); err != nil {
		logger.Warn("telemetry: consent install failed; telemetry will stay inert",
			"err", err)
	}

	telBus := bus.New()

	emitter, err := telemetry.New(
		telemetry.WithBus(telBus),
		telemetry.WithTopicPrefix(demoAppPrefix+".telemetry.event"),
		telemetry.WithKitVersion(demoVersion),
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

	return &TelemetryResources{Bus: telBus, Sink: sink}, nil
}
