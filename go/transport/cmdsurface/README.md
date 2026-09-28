# cmdsurface

## What it answers

How one cobra tree reaches every transport an adopter wants (REST,
ConnectRPC, WebSocket, SSE, MCP, webhooks, bus, cron, OAuth callback,
signed URL, FaaS, in-process) without rewriting the command logic per
transport. A `Bridge` wraps the cobra root; each `Mount*` projects the
leaves onto one surface, gated by one `Policy` and executed by one
`Runner`. REST is the `/v1/commands` command projection (the `api`
service on a kit root, `MountProjection` on a bare bridge) and MCP is
the official SDK (`go/transport/mcpsdk`, served by the `mcp`
service); `MountREST` and `MountMCP` are deprecated. The lifecycle
seam for a new transport is `go/transport/transportsvc`.

## Use it when

- project the tree onto REST → `MountProjection`, or the `api` service (`cli.WithAPI`) on a kit root
- project the tree onto RPC or streaming → `MountRPC`, `MountWS`, `MountSSE`
- expose leaves as LLM tools → `mcpsdk.Mount`, or the `mcp` service (`mcpserve.With`)
- accept third-party push or scheduled work → `MountWebhooks`, `MountBus`, `MountCron`
- issue a one-shot exec link or an OAuth callback → `MountSigned`, `MountOAuth`
- deploy the same leaves as a function → `LambdaHandler`, `RunCloudRun`
- invoke in-process from a REPL or test → `InvokeArgs`, `StreamArgs`
- gate a call before committing to a stream, then stream it → `Bridge.Admit`, `Admission.Stream`
- ask a person something between the gates and the run → `Bridge.Admit`, then `Admission.Run` or `Admission.Stream`
- answer a repeated `Idempotency-Key` from its record → `WithIdempotency(NewIdempotencyLedger(store), ttl)`; `Result.Replayed`, `Admission.Replayed`
- ask what the permission gate would answer a caller, without running or charging → `Bridge.Verdict`
- learn, inside a served run, who it runs for → `AdmittedMeta(cmd.Context())`
- count calls and bytes per key over fixed windows that survive a restart → `NewUsageLedger(kvStore)`; cap them per caller → `WithQuota`
- toggle a leaf per surface → `Bridge.Expose` / `Bridge.Hide`, or YAML `LoadFile` / `FromConfig`
- declare webhooks, bus bindings, schedules or sinks in YAML → `Config.WebhookMappings`, `BusBindings`, `CronSchedules`, `SinkSpecs`

## Quick start

```go
root := buildCobraTree()
b := cmdsurface.New(root)
b.Expose("*", cmdsurface.SurfaceMCP, cmdsurface.SurfaceWS)

r := api.NewRouter()
_ = mcpsdk.Mount(b, r)
_ = cmdsurface.MountWS(b, r)
_ = http.ListenAndServe(":8080", r)
```

## Contract

- Fourteen surfaces are declared: `cli`, `rest`, `ws`, `sse`, `rpc`, `mcp`, `webhook`, `bus`, `cron`, `lib`, `oauth-cb`, `signed`, `faas`, `socket`. `socket` is the Unix socket service (`cli.WithSocket`), distinct from `rpc` (ConnectRPC).
- `kit/side-effect=destructive` blocks every remote surface unless the surface is listed in `Policy.AllowDestructiveOn`; YAML `destructive_default: deny_remote` is the conservative default.
- `kit/auth-required` is a bridge gate on every remote surface: the leaf runs only when the transport established the caller (`Meta.Established`: a verifier accepted a credential, or the transport proves the caller, as the socket's `0600` file and the stdio spawn do), else `ErrAuthRefused`. A claimed `Meta.Caller` or a bare `Authorization` header never counts. `kit/requires-confirmation` gates every surface through the same `Policy`.
- Discovery answers per caller: `MountProjection`'s `GET /v1/commands` from a request an `api.Auth` verified asks `Bridge.Verdict` for each served command and lists a refusal as `invocable: false` with `insufficient-scope` or `permission-denied`; an anonymous request gets the shared listing. `Bridge.Verdict` asks the `PermissionFunc` on a `ProbeContext`, which must charge nothing.
- Webhook mappings targeting auth-required leaves with `AuthNone` are refused at mount; `WebhookAuth.Verify` runs before template execution.
- Signed URLs carry single-use nonces, an expiry, and the exact `Invocation` baked into the token; `AuthRequired` and `RequiresConfirmation` are skipped, the destructive ceiling still applies.
- Runners: `InProcessRunner` (shared tree, serialized), `InProcessRunner` with `WithRootFactory` (tree per invocation, parallel), `SubprocessRunner` (process per invocation, process-group cancellation on Unix).
- A served command knows it is served: the bridge puts the admitted `Meta` on the command's context (`AdmittedMeta`), whatever the runner; `SubprocessRunner` hands the child its `IdempotencyScope` as `KIT_IDEMPOTENCY_SCOPE`. `ScopeIdempotencyKey` turns a served `--idempotency-key` into a key confined to the caller (verified tenant and principal, else surface, claimed caller and client host); outside a served invocation it returns the key unchanged.
- Sinks shipped: Log, File, Webhook, Bus, Chain (tamper-evident, over `go/security`'s audit log; records the redacted invocation, never its output).
- `MountRPC` serves `cmdsurface.v1.Commands`, schema `contracts/proto/cmdsurface/v1/commands.proto`, Go stubs in `gen/cmdsurfacev1`; Connect, gRPC and gRPC-Web on one handler, gRPC over h2c via `rpc.ListenAndServe`. `WithRPCCallMeta`, `WithRPCAuthenticated` and `WithRPCHandlerOptions` wire a host's verified identity, auth-required gate and size bound (a bare `MountRPC` refuses auth-required leaves); the `rpc` service (`go/console/cli/rpcserve`) sets them all. `WithRPCAdmittedInterceptors` adds interceptors that see only admitted calls.

## Neighbours

- `go/transport/mcpsdk`: the MCP surface, replacing the deprecated `MountMCP`.
- `go/transport/api`: the router, the hub, and the automatic `/v1/commands` REST projection.
- `go/transport/transportsvc`: the serve-lifecycle seam for a transport of your own.
- `go/ai/cmdreflect`: the `Descriptor` reflection each leaf is built from.
- `examples/cmdsurface/`, `examples/cmdsurface-faas/`: every surface in one binary; end-to-end tests behind the `e2e` build tag.

## See also

- [cmdsurface reference](../../../docs/adopters/reference/cmdsurface.md): concepts, reflection, execution, the surface matrix, every `Mount*` and its options, sinks, telemetry, the safety matrix, YAML config, patterns, threat model, status
- [cmdsurface example walkthrough](../../../docs/adopters/guides/cmdsurface-example.md)
- [serve lifecycle contract](../../../docs/contracts/serve-lifecycle.md)
