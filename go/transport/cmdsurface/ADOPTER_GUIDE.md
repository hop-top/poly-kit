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

## MCP auth hardening (spec 2026-07-28)

The MCP surface is auth-scheme-agnostic: `Class.AuthRequired` leaves
are gated on `Authorization` header presence only. The 2026-07-28
authorization obligations bind the authorization server / resource
server deployed in front of the mount — not the transport bridge:

- **RFC 9207 issuer identification** — your authorization server
  must emit `iss` on every authorization response, error responses
  included. Kit-side, the OAuth callback surface enforces the client
  half: set `OAuthProvider.ExpectedIssuer` to the provider's issuer
  identifier and callbacks reject responses whose `iss` is missing
  or differs from it (exact string match), before state consumption.
- **`application_type` at client registration** — register OAuth
  clients with an explicit `application_type`. Kit performs no
  client registration (neither a DCR client nor a registration
  endpoint); this lives entirely in your authorization server or
  registration tooling.
- **Credential binding** — bind issued tokens to the issuing
  authorization server and reject cross-issuer presentation at your
  resource server. Kit forwards the RFC 9207-validated issuer to
  sinks and custom Runners via `Meta.Extra["oauth_issuer"]`, and to
  the leaf via a `FlagFromQuery` `"iss"` mapping, so credential
  stores can record the binding at mint time.
- **CIMD over DCR** — the spec deprecates Dynamic Client
  Registration in favor of Client ID Metadata Documents (a hosted
  metadata URL as the client identifier). Choose this at your
  authorization server; kit holds no client identity and needs no
  change. Existing DCR-based deployments keep working.

```go
providers := []cmdsurface.OAuthProvider{{
    Name:           "github",
    Path:           []string{"auth", "oauth-link"},
    FlagFromQuery:  map[string]string{"code": "code", "iss": "issuer"},
    ExpectedIssuer: "https://as.example", // RFC 9207
}}
```

## MCP mid-call confirmation (spec 2026-07-28)

`kit/requires-confirmation` leaves on the modern MCP path default to
the `X-Confirm-Token` header gate. Opt into the spec-native MRTR
confirmation round-trip by giving the mount key material:

```go
_ = cmdsurface.MountMCP(b, r,
    cmdsurface.WithMCPConfirmationKey(key)) // non-empty, shared across instances
```

With a key configured, clients declaring the `elicitation` capability
in `_meta` receive `resultType: "input_required"` carrying a
confirmation prompt (`inputRequests.confirm`, an `elicitation/create`
form request) and an HMAC-SHA-256-protected `requestState`. The retry
echoes the state and answers `accept` to run the leaf; `decline` /
`cancel` refuse it. The state binds the leaf, its arguments, and the
caller's `Authorization` value, and expires after five minutes:
expiry is a routine re-prompt, while a state failing verification is
never honored — the rejection is emitted to registered `OnError`
sinks as a security-relevant audit event before a fresh prompt is
issued. Clients without the capability keep the header gate, and the
destructive ceiling (`Policy.AllowDestructiveOn`) is never relaxed by
a confirmation outcome.

Key sourcing is deliberately explicit — there is no generated
default. Give every instance behind a load balancer the same key, or
retries landing on a different instance will be refused and
re-prompted.

## Telemetry opt-in

`Config.Telemetry` is `nil` by default — no events leave the binary
unless an adopter explicitly opts in. Wire an emitter provider and
flip `Enabled` to ship invocation summaries through kit-telemetry.

```go
cfg := cmdsurface.Config{
    Telemetry: &cmdsurface.TelemetryConfig{
        Enabled: true,
        Mode:    "anon", // or "full"
    },
    TelemetryEmitterProvider: func() (*telemetry.Emitter, error) {
        return telemetry.New(
            telemetry.WithBus(myBus),
            telemetry.WithTopicPrefix("myapp.telemetry.event"),
        )
    },
}
bridge, err := cmdsurface.FromConfig(rootCmd, cfg)
defer bridge.Close(ctx) // drains in-flight events
```

Operators control consent and inspect captured events via
`kit telemetry status | enable | disable | reset | inspect`.

See [README.md](README.md) "Telemetry sink" for the full reference
(Anon vs Full, size cap, trace correlation, non-blocking guarantees).

## Reference

See [README.md](README.md) for package reference.
