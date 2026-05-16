// Command cmdsurface-example demonstrates a single cobra command tree
// projected onto every supported invocation surface — CLI, REST, RPC,
// MCP, WebSocket, SSE, Bus, Cron, Library — via go/transport/cmdsurface.
// With no arguments it starts an HTTP server on :8080 (REST + MCP + WS +
// SSE) and a ConnectRPC server on :8081, plus an in-process bus and cron
// engine. With arguments it executes the cobra tree locally and exits.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	log := slog.Default()

	// CLI mode: arguments beyond the program name → execute cobra and exit.
	// We build a fresh tree here so the CLI does not pay the cost of the
	// bridge / surface wiring.
	if len(os.Args) > 1 {
		root := buildCobraTree()
		if err := root.ExecuteContext(ctx); err != nil {
			os.Exit(1)
		}
		return
	}

	app, err := BuildExample(ctx, log)
	if err != nil {
		log.Error("BuildExample", "err", err)
		os.Exit(1)
	}
	defer app.Cleanup()

	errCh := make(chan error, 2)
	go func() { errCh <- app.HTTPSrv.ListenAndServe() }()
	go func() { errCh <- app.RPCHTTP.ListenAndServe() }()
	log.Info("servers started",
		"rest+mcp+ws+sse", "http://localhost:8080",
		"rpc", "http://localhost:8081",
		"openapi", "http://localhost:8080/openapi.json",
		"ws", "ws://localhost:8080/ws/cmd",
	)

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}

	_ = app.HTTPSrv.Shutdown(context.Background())
	_ = app.RPCHTTP.Shutdown(context.Background())
}
