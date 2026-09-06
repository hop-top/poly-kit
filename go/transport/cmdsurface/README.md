# cmdsurface

## What it answers

How one cobra tree reaches every transport an adopter wants (REST,
ConnectRPC, WebSocket, SSE, MCP, webhooks, bus, cron, OAuth callback,
signed URL, FaaS, in-process) without rewriting the command logic per
transport. A `Bridge` wraps the cobra root; each `Mount*` projects the
leaves onto one surface, gated by one `Policy` and executed by one
`Runner`. Serving MCP through the official SDK instead is
`go/transport/mcpsdk`; the lifecycle seam for a new transport is
`go/transport/transportsvc`.

## Use it when

- project the tree onto HTTP, RPC or streaming → `MountREST`, `MountRPC`, `MountWS`, `MountSSE`
- expose leaves as LLM tools → `MountMCP`
- accept third-party push or scheduled work → `MountWebhooks`, `MountBus`, `MountCron`
- issue a one-shot exec link or an OAuth callback → `MountSigned`, `MountOAuth`
- deploy the same leaves as a function → `LambdaHandler`, `RunCloudRun`
- invoke in-process from a REPL or test → `InvokeArgs`, `StreamArgs`
- toggle a leaf per surface → `Bridge.Expose` / `Bridge.Hide`, or YAML `LoadFile` / `FromConfig`

## Quick start

```go
root := buildCobraTree()
b := cmdsurface.New(root)
b.Expose("*", cmdsurface.SurfaceREST, cmdsurface.SurfaceMCP, cmdsurface.SurfaceWS)

r := api.NewRouter()
_ = cmdsurface.MountREST(b, r)
_ = cmdsurface.MountMCP(b, r)
_ = cmdsurface.MountWS(b, r)
_ = http.ListenAndServe(":8080", r)
```

## Contract

- Thirteen surfaces are declared: `cli`, `rest`, `ws`, `sse`, `rpc`, `mcp`, `webhook`, `bus`, `cron`, `lib`, `oauth-cb`, `signed`, `faas`.
- `kit/side-effect=destructive` blocks every remote surface unless the surface is listed in `Policy.AllowDestructiveOn`; YAML `destructive_default: deny_remote` is the conservative default.
- `kit/auth-required` and `kit/requires-confirmation` gate every surface through the same `Policy`.
- Webhook mappings targeting auth-required leaves with `AuthNone` are refused at mount; `WebhookAuth.Verify` runs before template execution.
- Signed URLs carry single-use nonces, an expiry, and the exact `Invocation` baked into the token; `AuthRequired` and `RequiresConfirmation` are skipped, the destructive ceiling still applies.
- Runners: `InProcessRunner` (shared tree, serialized), `InProcessRunner` with `WithRootFactory` (tree per invocation, parallel), `SubprocessRunner` (process per invocation, process-group cancellation on Unix).
- Sinks shipped: Log, File, Webhook, Bus.

## Neighbours

- `go/transport/mcpsdk`: the SDK-backed MCP surface, an alternative to `MountMCP`.
- `go/transport/api`: the router, the hub, and the automatic `/v1/commands` REST projection.
- `go/transport/transportsvc`: the serve-lifecycle seam for a transport of your own.
- `go/ai/cmdreflect`: the `Descriptor` reflection each leaf is built from.
- `examples/cmdsurface/`, `examples/cmdsurface-faas/`: every surface in one binary; end-to-end tests behind the `e2e` build tag.

## See also

- [cmdsurface reference](../../../docs/adopters/reference/cmdsurface.md): concepts, reflection, execution, the surface matrix, every `Mount*` and its options, sinks, telemetry, the safety matrix, YAML config, patterns, threat model, status
- [cmdsurface example walkthrough](../../../docs/adopters/guides/cmdsurface-example.md)
- [serve lifecycle contract](../../../docs/contracts/serve-lifecycle.md)
