// Package shared builds a sample [cmdsurface.Bridge] used by both
// the Lambda and Cloud Run FaaS demos. The two cmd/* binaries import
// this package so they share an identical tree and policy — the only
// difference between the two targets is the adapter that fronts the
// bridge.
package shared

import (
	"context"
	"time"

	"github.com/spf13/cobra"
	"hop.top/kit/go/transport/cmdsurface"
)

// BuildOption configures BuildBridge. Tests pass no options to get
// the historical zero-arg behavior; the FaaS cmd/* binaries pass
// WithTelemetrySink to fan invocation outcomes into kit-telemetry.
type BuildOption func(*buildConfig)

type buildConfig struct {
	telemetrySink *cmdsurface.TelemetrySink
}

// WithTelemetrySink wraps the bridge's default InProcessRunner with a
// sink fan-out runner that pushes each Result through the supplied
// TelemetrySink. Pass nil to opt out (no-op); pass a sink returned by
// MaybeBuildTelemetry to enable the kit-telemetry pipeline.
func WithTelemetrySink(s *cmdsurface.TelemetrySink) BuildOption {
	return func(c *buildConfig) { c.telemetrySink = s }
}

// BuildBridge returns a Bridge over a tiny demo command tree.
//
// Tree:
//
//	echo <message>    [safe] — prints the message back
//	ping              [safe] — prints "pong"
//	stamp --who=X     [safe] — prints "stamped by X at <now>"
//
// All three leaves are read-only, so the bridge enables every surface
// by default. AllowDestructiveOn is set anyway so adopters copying
// this example can add destructive leaves without surprises.
//
// Pass WithTelemetrySink to wire the optional kit-telemetry pipeline
// in. Tests historically called this with no args and continue to do
// so — the variadic shape keeps the contract backwards-compatible.
func BuildBridge(opts ...BuildOption) *cmdsurface.Bridge {
	cfg := buildConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}

	root := buildTree()

	bridgeOpts := []cmdsurface.Option{
		cmdsurface.WithPolicy(cmdsurface.Policy{
			DefaultEnabled: []cmdsurface.Surface{
				cmdsurface.SurfaceCLI,
				cmdsurface.SurfaceFaaS,
				cmdsurface.SurfaceREST,
				cmdsurface.SurfaceSSE,
				cmdsurface.SurfaceMCP,
				cmdsurface.SurfaceWS,
				cmdsurface.SurfaceLib,
			},
			AllowDestructiveOn: []cmdsurface.Surface{
				cmdsurface.SurfaceCLI,
				cmdsurface.SurfaceLib,
			},
		}),
	}

	// Wrap the default InProcessRunner with a sink-runner when a
	// telemetry sink was supplied. Inline here to avoid carrying the
	// `examples/cmdsurface/sinkrunner.go` type across packages — the
	// helper is small and the FaaS demos do not currently wire any
	// other sinks.
	if cfg.telemetrySink != nil {
		inner := cmdsurface.InProcessRunner(root)
		sinks := cmdsurface.SinkSet{
			{
				Sink:    cfg.telemetrySink,
				OnError: true,
				OnOK:    true,
			},
		}
		bridgeOpts = append(bridgeOpts, cmdsurface.WithRunner(&sinkFanOutRunner{
			inner: inner,
			sinks: sinks,
		}))
	}

	return cmdsurface.New(root, bridgeOpts...)
}

// sinkFanOutRunner is a minimal Runner that delegates to inner and
// fans each completed (inv, res, err) tuple through sinks. Stream
// invocations bypass the fan-out — streaming sinks would require a
// different contract and the FaaS demos do not exercise it.
type sinkFanOutRunner struct {
	inner cmdsurface.Runner
	sinks cmdsurface.SinkSet
}

func (s *sinkFanOutRunner) Run(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	res, err := s.inner.Run(ctx, inv)
	_ = s.sinks.Emit(ctx, inv, res, err)
	return res, err
}

func (s *sinkFanOutRunner) Stream(ctx context.Context, inv cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
	return s.inner.Stream(ctx, inv, out)
}

// buildTree returns the demo cobra tree. The root is non-runnable;
// each child is a leaf that writes to cmd.OutOrStdout() so the bridge
// captures the output into Result.Stdout.
func buildTree() *cobra.Command {
	root := &cobra.Command{
		Use:   "demo",
		Short: "cmdsurface-faas demo tree",
	}

	echo := &cobra.Command{
		Use:   "echo <message>",
		Short: "Echo the given message back",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			for i, a := range args {
				if i > 0 {
					cmd.Print(" ")
				}
				cmd.Print(a)
			}
			cmd.Println()
			return nil
		},
	}

	ping := &cobra.Command{
		Use:   "ping",
		Short: "Reply with pong",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println("pong")
			return nil
		},
	}

	stamp := &cobra.Command{
		Use:   "stamp",
		Short: "Print a timestamp tagged with --who",
		RunE: func(cmd *cobra.Command, _ []string) error {
			who, _ := cmd.Flags().GetString("who")
			if who == "" {
				who = "anon"
			}
			cmd.Printf("stamped by %s at %s\n", who, time.Now().Format(time.RFC3339))
			return nil
		},
	}
	stamp.Flags().String("who", "", "name to stamp with")

	root.AddCommand(echo, ping, stamp)
	return root
}
