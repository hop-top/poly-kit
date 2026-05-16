package cli_test

// Gap tests for `hop.top/kit/go/console/cli` — extension surface
// (WithAPI multi-server). Separate file from gaps_test.go so the
// dpkms-surfaced gaps stay grouped.

import (
	"testing"

	"hop.top/kit/go/console/cli"
)

// Gap: cli.WithAPI is the spec'd HTTP-server scaffolding, but it
// only models a single listener.
//
// dpkms's serve.go is 660 LOC and predates WithAPI; it composes:
//
//   - HTTP API listener
//   - gRPC listener
//   - WebSocket bus listener
//   - cookie-bridge listener
//
// on independent addresses, with shared lifecycle (graceful drain on
// SIGTERM) and shared auth context. WithAPI's APIConfig today carries
// one Addr — there is no spec for "register N listeners on one Root,
// share lifecycle, share auth". So dpkms (and any tool with multiple
// transports) skips WithAPI and rolls its own serve.go.
//
// Desired API:
//
//	cli.New(cfg,
//	    cli.WithAPI(httpCfg),
//	    cli.WithGRPC(grpcCfg),
//	    cli.WithBus(busCfg),
//	    cli.WithCookieBridge(bridgeCfg),
//	)
//
// Each WithX option registers a listener; Root.Execute() drives one
// errgroup and one drain. Or a single composing option:
//
//	cli.WithServers(cli.ServerSet{API: ..., GRPC: ..., Bus: ..., Bridge: ...})
//
// — design TBD; the gap is "no path through cli for multi-server
// composition".
func TestGap_WithAPI_MultiServer_Missing(t *testing.T) {
	t.Skip("gap: cli.WithAPI is single-listener; no multi-server composition (HTTP+gRPC+WS+bridge) — dpkms's 660 LOC serve.go predates the spec")

	// Pin: WithAPI exists and works for one listener. The gap is
	// the absence of a sibling option (or an extended APIConfig)
	// that registers additional transports under a shared
	// lifecycle.
	_ = cli.WithAPI(cli.APIConfig{})
}
