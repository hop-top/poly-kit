# cmdsurface example

## What it answers

How one cobra command tree is projected onto every invocation surface
kit supports (CLI, REST, ConnectRPC, MCP, WebSocket, SSE, Bus, Cron,
Library, Webhook, OAuth callback, Signed URL) through the
`hop.top/kit/go/transport/cmdsurface` bridge, and how every Result fans
out through a `SinkSet` for logging, auditing and replay. For managed
runtimes (Lambda, Cloud Run) use
[`examples/cmdsurface-faas`](../cmdsurface-faas/README.md) instead.

## Use it when

- you want to explore the surface matrix on your laptop → run the binary
  and follow the per-surface walkthrough
- you need a sink fan-out idiom → copy `sinkrunner.go`
- you need webhook HMAC, OAuth state or signed-URL wiring → read
  `setup.go` and the security notes in the walkthrough
- you want the telemetry pipeline as a sink → set
  `CMDSURFACE_DEMO_TELEMETRY=1` and read `telemetry.go`

## Quick start

```sh
# Start the servers (REST + MCP + WS + SSE on :8080, RPC on :8081).
go run ./examples/cmdsurface

# Invoke the same tree locally — arguments after the program name
# switch into CLI mode.
go run ./examples/cmdsurface widget add --name foo --tag a --tag b
go run ./examples/cmdsurface tick --count 3 --interval 100ms
```

OpenAPI spec: <http://localhost:8080/openapi.json>

End-to-end tests:

```sh
go test -tags=e2e -race -count=1 ./examples/cmdsurface/...
```

## Contract

- Tree: `widget add/list/get/delete`, `report generate/purge`,
  `subscription cancel`, `auth oauth-link`, `notify message`, `ping`,
  `tick`, defined inline.
- Destructive leaves (`widget delete`, `report purge`) run only on the
  CLI and Library surfaces; every remote surface refuses with
  `destructive_blocked` / `PERMISSION_DENIED`.
- `widget delete` is hidden from every remote surface: absent from the
  OpenAPI spec and the MCP `tools/list`.
- `cmdsurface` never calls sinks itself; `sinkRunner` wraps
  `InProcessRunner` and emits each Result to a `LogSink` and a `FileSink`.
- Telemetry is inert unless `CMDSURFACE_DEMO_TELEMETRY=1` and the operator
  has run `kit telemetry enable`; the emitter ships only the bounded
  canonical fields in `ModeAnon`.
- CLI mode bypasses the bridge entirely (see `main.go`).

## Neighbours

- `go/transport/cmdsurface`: the bridge, surfaces, sinks and FaaS adapters
- `examples/cmdsurface-faas`: the same bridge behind Lambda and Cloud Run
- `go/runtime/telemetry`: emitter and consent store used by `telemetry.go`

## See also

- [cmdsurface example walkthrough](../../docs/adopters/guides/cmdsurface-example.md):
  per-surface commands, security notes, sinks, destructive-block table,
  telemetry wiring
- [Migrate to served commands](../../docs/adopters/guides/migrate-to-served-commands.md)
- [Secure remote serving](../../docs/adopters/guides/secure-remote-serving.md)
