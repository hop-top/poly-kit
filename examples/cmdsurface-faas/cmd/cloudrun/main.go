// Command cmdsurface-faas-cloudrun demonstrates the Cloud Run adapter
// pattern: the bridge is built once at process start and
// [cmdsurface.RunCloudRun] handles the lifecycle (signal-driven
// shutdown, $PORT discovery, surface mounting).
//
// Run locally:
//
//	go run ./examples/cmdsurface-faas/cmd/cloudrun
//	curl http://localhost:8080/cmd/ping            # REST  → {exit_code:0, stdout:"pong\n"}
//	curl http://localhost:8080/cmd/ping/stream     # SSE   → event stream
//
// Deploy to Cloud Run from the repo root (uses the Dockerfile in
// this directory):
//
//	gcloud run deploy cmdsurface-faas-demo \
//	    --source . \
//	    --region us-central1 \
//	    --allow-unauthenticated
package main

import (
	"context"
	"log"
	"log/slog"
	"time"

	"hop.top/kit/examples/cmdsurface-faas/shared"
	"hop.top/kit/go/transport/cmdsurface"
)

func main() {
	logger := slog.Default()

	// Optional kit-telemetry wiring. Disabled by default — set
	// CMDSURFACE_DEMO_TELEMETRY=1 in the Cloud Run service's env to
	// enable. The pipeline is constructed before the bridge so the
	// telemetry sink is attached to the bridge's runner from the
	// first request onward.
	telRes, _ := shared.MaybeBuildTelemetry(logger)
	defer func() {
		if telRes != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			telRes.Close(ctx)
		}
	}()

	var buildOpts []shared.BuildOption
	if telRes != nil && telRes.Sink != nil {
		buildOpts = append(buildOpts, shared.WithTelemetrySink(telRes.Sink))
	}
	bridge := shared.BuildBridge(buildOpts...)

	err := cmdsurface.RunCloudRun(bridge, cmdsurface.CloudRunConfig{
		Surfaces: cmdsurface.CloudRunSurfaces{
			REST: true,
			SSE:  true,
			MCP:  true,
		},
		OnReady:    func(addr string) { log.Printf("ready on %s", addr) },
		OnShutdown: func() { log.Print("shutting down") },
	})
	if err != nil {
		log.Fatal(err)
	}
}
