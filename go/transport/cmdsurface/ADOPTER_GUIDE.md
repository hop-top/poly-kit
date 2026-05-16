# Adopter quickstart

`cmdsurface` projects a single `*cobra.Command` tree onto multiple
transport surfaces. Build your CLI once; expose it on CLI + REST +
MCP + WS + … without rewriting handlers.

## Surfaces

| Surface | Direction | Mount |
|---------|-----------|-------|
| CLI | local | cobra (unchanged) |
| REST | request/reply | `MountREST(b, r)` |
| RPC | request/reply (ConnectRPC) | `MountRPC(b, s)` |
| MCP | LLM tool exec | `MountMCP(b, r)` |
| WS | bidirectional stream | `MountWS(b, r)` |
| SSE | server stream | `MountSSE(b, r)` |
| Bus | pub/sub | `MountBus(b, sub, pub, bindings)` |
| Cron | scheduled | `MountCron(b, engine, schedules)` |
| Library | in-process | `InvokeArgs(ctx, b, argv)` |
| Webhook | inbound HTTP | `MountWebhooks(b, r, mappings)` |
| OAuth callback | inbound HTTP | `MountOAuth(b, r, providers, store)` |
| Signed URL | inbound HTTP | `MountSigned(b, r, key, store)` |
| FaaS | AWS Lambda | `LambdaHandler(b, cfg)` |
| FaaS | Cloud Run | `RunCloudRun(b, cfg)` |

## Minimal example

```go
import (
    "hop.top/kit/go/transport/api"
    "hop.top/kit/go/transport/cmdsurface"
)

// Build the bridge from your existing cobra root.
b := cmdsurface.New(rootCmd)

// Mount surfaces.
r := api.NewRouter()
_ = cmdsurface.MountREST(b, r)
_ = cmdsurface.MountMCP(b, r)
_ = cmdsurface.MountWS(b, r)

http.ListenAndServe(":8080", r)
```

## Safety

Destructive commands (`kit/side-effect=destructive` cobra annotation)
are blocked from remote surfaces by default. Opt in explicitly via
`Policy.AllowDestructiveOn`.

## Reference

See [README.md](README.md) for package reference.
