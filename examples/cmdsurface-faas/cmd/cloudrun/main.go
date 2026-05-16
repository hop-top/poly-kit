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
	"log"

	"hop.top/kit/examples/cmdsurface-faas/shared"
	"hop.top/kit/go/transport/cmdsurface"
)

func main() {
	bridge := shared.BuildBridge()
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
