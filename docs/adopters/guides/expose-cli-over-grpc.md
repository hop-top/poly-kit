# Expose your CLI over gRPC

Serve your cobra tree as a typed RPC service that Connect, gRPC and
gRPC-Web clients call on one port, with no mounting code.

## Who this is for

Developers building a kit CLI who want other services, other
languages, or a browser to call their commands through generated,
typed stubs. Registering the `rpc` service is the whole setup: the
tree is reflected when the server starts, and every conformant
command is reachable through one published service,
`cmdsurface.v1.Commands`. For plain HTTP and OpenAPI, see
[expose-cli-over-rest.md](expose-cli-over-rest.md); for LLM hosts, see
[expose-cli-over-mcp.md](expose-cli-over-mcp.md).

## Before you begin

You need:

- A kit project with a cobra root (see
  [create-cli-project.md](create-cli-project.md))
- `hop.top/kit/go/console/cli/rpcserve` importable
- Commands annotated with `kit/side-effect`; the annotation gates what
  may run remotely
- [`buf`](https://buf.build/docs/installation) to call the service from
  a shell in the examples below

## What you get

`rpcserve.With` serves **one service for your whole command tree**,
defined in
[`contracts/proto/cmdsurface/v1/commands.proto`](../../../contracts/proto/cmdsurface/v1/commands.proto):

- **`Invoke(Invocation) → Result`**: run a command to completion
- **`InvokeStream(Invocation) → stream Event`**: run it and receive
  each output line as it is written, then the result

The proto file is the contract. Commands are addressed by path inside
the `Invocation` rather than by procedure, so adding a command to your
tree needs no new stubs: it is callable the next time the server
starts. Go stubs ship with kit in
`go/transport/cmdsurface/gen/cmdsurfacev1`; any other language
generates its own from the proto file.

One listener answers Connect (binary proto and JSON), gRPC and
gRPC-Web, over HTTP/1.1 and unencrypted HTTP/2 (h2c) on the same port,
or over TLS with HTTP/2 negotiated by ALPN once `services.rpc.tls` is
set.

## Steps

### 1. Register the rpc service

```go
package main

import (
    "context"

    "hop.top/kit/go/console/cli"
    "hop.top/kit/go/console/cli/rpcserve"
)

func main() {
    root := cli.New(cli.Config{Name: "mytool", Version: "1.4.2"},
        rpcserve.With(rpcserve.Config{}), // listens on 127.0.0.1:8082
    )
    _ = root.Execute(context.Background())
}
```

```bash
mytool serve rpc
```

```text
INFO serve: ready_reported object=service service=rpc address=http://127.0.0.1:8082
```

The service is off until you name it: `serve rpc` starts it alone,
`serve --enable rpc` starts it beside every enabled service (the `api`
service, when you also registered `WithAPI`), and
`services.rpc.enabled: true` in config makes a bare `serve` start it.
`--rpc-addr` overrides the address for one run.

The service lives in its own package so that a CLI that never serves
RPC does not link the RPC server.

### 2. Call a command

Every call names the command by path, with its flags and positional
arguments:

```bash
buf curl --schema contracts/proto/cmdsurface/v1 \
  --data '{"path":["item","add"],"args":["washer"]}' \
  http://127.0.0.1:8082/cmdsurface.v1.Commands/Invoke
```

```json
{
  "exit_code": 0,
  "stdout": "added washer\n"
}
```

`--schema` points at the proto directory in a kit checkout; outside
one, copy `commands.proto` beside your client. The server offers no
reflection service, so every client is given the schema.

A command that declares an output schema answers in `data`, its
output decoded, and in `data_json`, the same payload as JSON text with
every digit of every number intact:

```bash
buf curl --schema contracts/proto/cmdsurface/v1 \
  --data '{"path":["item","list"]}' \
  http://127.0.0.1:8082/cmdsurface.v1.Commands/Invoke
```

```json
{
  "exit_code": 0,
  "data": [{"name": "bolt"}, {"name": "nut"}],
  "data_json": "[{\"name\":\"bolt\"},{\"name\":\"nut\"}]"
}
```

A command that ran and failed is still a successful call: read
`exit_code`. A flag the command does not take, for example, answers
`exit_code: 2` with the parser's message in `stderr`. A call the
service refuses before anything runs is an RPC error instead; see
[Refusals](#refusals).

### 3. Call it over native gRPC

gRPC needs HTTP/2. Without TLS, the client speaks h2c, which gRPC
clients call plaintext or insecure mode:

```bash
buf curl --schema contracts/proto/cmdsurface/v1 \
  --protocol grpc --http2-prior-knowledge \
  --data '{"path":["item","list"]}' \
  http://127.0.0.1:8082/cmdsurface.v1.Commands/Invoke
```

Any gRPC client works the same way: dial `127.0.0.1:8082` without
transport security, with stubs generated from `commands.proto`.

### 4. Call it from Go

The generated Connect client speaks all three protocols:

```go
import (
    "context"
    "net/http"

    "connectrpc.com/connect"

    "hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1"
    "hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1/cmdsurfacev1connect"
)

func listItems(ctx context.Context) (string, error) {
    client := cmdsurfacev1connect.NewCommandsClient(
        http.DefaultClient, "http://127.0.0.1:8082",
    )
    res, err := client.Invoke(ctx, connect.NewRequest(&cmdsurfacev1.Invocation{
        Path: []string{"item", "list"},
    }))
    if err != nil {
        return "", err // a refusal: connect.CodeOf(err) says which
    }
    return res.Msg.GetDataJson(), nil
}
```

For native gRPC, pass `connect.WithGRPC()` and an HTTP client that
speaks h2c:

```go
protocols := new(http.Protocols)
protocols.SetUnencryptedHTTP2(true)
h2c := &http.Client{Transport: &http.Transport{Protocols: protocols}}

client := cmdsurfacev1connect.NewCommandsClient(
    h2c, "http://127.0.0.1:8082", connect.WithGRPC(),
)
```

Flags travel as a `google.protobuf.Struct`; build it with
`structpb.NewStruct(map[string]any{"limit": 5})`.

### 5. Call it from a browser over gRPC-Web

Generate TypeScript from `commands.proto` with
[`protoc-gen-es`](https://github.com/bufbuild/protobuf-es) and call it
through Connect-Web's gRPC-Web transport:

```ts
import { createClient } from "@connectrpc/connect";
import { createGrpcWebTransport } from "@connectrpc/connect-web";
import { Commands } from "./gen/commands_pb";

const transport = createGrpcWebTransport({ baseUrl: "http://127.0.0.1:8082" });
const client = createClient(Commands, transport);

const res = await client.invoke({ path: ["item", "list"] });
console.log(res.dataJson);
```

The service does not answer CORS yet, so a browser only reaches it
from the same origin. For a page on another origin, put a proxy in
front that answers the preflight (`OPTIONS`) and adds the CORS headers:
allow your page's origin, `GET` and `POST`, and the request headers
Connect and gRPC-Web clients send (`Content-Type`,
`Connect-Protocol-Version`, `Connect-Timeout-Ms`, `Grpc-Timeout`,
`X-Grpc-Web`, `X-User-Agent`, plus `Authorization` and
`X-Confirm-Token` if you use them), and expose `Grpc-Status`,
`Grpc-Message` and `Grpc-Status-Details-Bin` so the client can read
errors. [connectrpc.com/cors](https://pkg.go.dev/connectrpc.com/cors)
lists the same values if your proxy is written in Go.
`createConnectTransport` works against the same port if you prefer the
Connect protocol.

### 6. Stream a long-running command

`InvokeStream` delivers each line the command writes as a `stdout` or
`stderr` event, then one `done` event carrying the result:

```bash
buf curl --schema contracts/proto/cmdsurface/v1 --protocol grpcweb \
  --data '{"path":["item","watch"],"flags":{"count":2,"interval":"1s"}}' \
  http://127.0.0.1:8082/cmdsurface.v1.Commands/InvokeStream
```

```json
{"kind": "stdout", "data": "tick 1: 2 items", "at": "2026-09-28T04:28:52.186894Z"}
{"kind": "stdout", "data": "tick 2: 2 items", "at": "2026-09-28T04:28:53.187933Z"}
{"kind": "done", "data": {"exit_code": 0, "stdout": "tick 1: 2 items\ntick 2: 2 items\n"}, "at": "2026-09-28T04:28:53.187971Z", "result": {"exit_code": 0, "stdout": "tick 1: 2 items\ntick 2: 2 items\n"}}
```

Typed clients read `result`; `data` repeats it for JSON clients.

Every command can be streamed; nothing to register. What to rely on:

- **Refusals come before the first event.** A stream the service
  refuses ends with the refusal as its error and no event sent.
- **A stream may run as long as the command does.** The server's
  write timeout (`services.rpc.timeouts.write`, 10 seconds by
  default) applies to unary calls only. A deadline you set —
  `kit/timeout` on the command, or `services.rpc.timeouts.command` —
  bounds both, and a call past it fails `DeadlineExceeded`.
- **Closing the stream cancels the command.** To stop when the
  client goes away, your command watches `cmd.Context()`. Stopping the
  service ends open streams too.
- **Long streams want a root factory.** Without
  `cli.WithRootFactory`, a running stream holds the tool's command
  tree and every other call waits for it; see
  [step 10 of the REST guide](expose-cli-over-rest.md#10-run-requests-in-parallel).

### 7. Permit a destructive command

Destructive commands are refused over RPC by default:

```json
{"code": "permission_denied",
 "message": "cmdsurface: destructive command blocked on this surface: item purge on rpc"}
```

Permit them by naming the RPC surface:

```go
import "hop.top/kit/go/transport/cmdsurface"

rpcserve.With(rpcserve.Config{
    Policy: cmdsurface.Policy{
        AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceRPC},
    },
})
```

That lifts the transport's ceiling; the command's own confirmation
still applies. An unconfirmed call now runs and is refused by the
command, with a non-zero `exit_code`. Send the confirmation as a flag:

```json
{"path": ["item", "purge"], "flags": {"confirm": "yes"}}
```

Naming `SurfaceRPC` widens RPC only: REST, MCP and the socket keep
their own ceilings.

### 8. Satisfy confirmation-gated commands

A command annotated `kit/requires-confirmation` needs an
`X-Confirm-Token` header on every call:

```bash
buf curl --schema contracts/proto/cmdsurface/v1 \
  -H 'X-Confirm-Token: yes' \
  --data '{"path":["item","tag"],"flags":{"name":"bolt"}}' \
  http://127.0.0.1:8082/cmdsurface.v1.Commands/Invoke
```

Without it the call is `failed_precondition` with the message
`confirmation_required`, and nothing runs.

### 9. Keep commands off RPC

`Expose` and `Hide` take command patterns and apply to RPC only:

```go
rpcserve.With(rpcserve.Config{
    Expose: []string{"item *"},      // only these; empty means the whole tree
    Hide:   []string{"item purge"},  // carved out after Expose
})
```

A hidden command answers `not_found`. Interactive, self-hosting and
management-only commands (`shell`, `serve`, `status`) never run over
RPC, exactly as they are never mounted over REST.

### 10. Enable auth and limits

Name a verifier in configuration and the service checks every call
with it, unary and streaming, before anything else; with one it may
listen beyond loopback:

```yaml
# ~/.config/mytool/config.yaml
services:
  rpc:
    enabled: true
    addr: 0.0.0.0:8443
    tls:                                   # native gRPC clients need TLS or h2c
      cert_file: /etc/mytool/tls/server.crt
      key_file: /etc/mytool/tls/server.key
    auth:
      mode: mtls                           # or jwt, jwks, oidc, apikey
      mtls:
        ca_file: /etc/mytool/tls/clients-ca.crt
    rate_limit:
      enabled: true                        # already on beyond loopback
    timeouts:
      command: 1m
```

```bash
mytool serve rpc --policy=readonly
```

A refused call is `unauthenticated` and is recorded in the audit
trail. A limit answers with a code a gRPC client already retries on:
`resource_exhausted` over the rate limit or the quota, `unavailable`
when every slot and the queue are full, each with `Retry-After` in the
error metadata, and `deadline_exceeded` past `timeouts.command`.
Beyond loopback the rate limit is on without configuration, and on
every bind the service caps a request at 4 MiB, times out slow clients
(`InvokeStream` is exempt from the write timeout) and runs 32 calls at
once with 64 queued. The block above only tunes them; every key, its
default and each refusal per surface is in
[served-middleware.md](../reference/served-middleware.md).

To verify calls yourself, set `rpcserve.Config.Auth`, the same
`api.AuthFunc` the REST service takes, so one function authenticates
both. It applies when no `auth.mode` is configured:

```go
import (
    "net/http"

    "hop.top/kit/go/transport/api"
)

rpcserve.With(rpcserve.Config{
    Addr: "0.0.0.0:8082",
    Auth: func(r *http.Request) (any, error) {
        claims, err := validateToken(r.Header.Get("Authorization"))
        if err != nil {
            return nil, err
        }
        return api.Claims{Subject: claims.User, Tenant: claims.Org, Scopes: claims.Scopes}, nil
    },
})
```

The function sees the call's headers and peer address; it must not
read the URL or body.

Identity comes only from what the verifier established. The
`meta.caller`, `meta.tenant` and `meta.extra` a client puts in the
request body are dropped, so no client can name itself a principal.
A command annotated `kit/auth-required` runs only for a verified
call; without a verifier it is refused, whatever header the client
sends.

Beyond loopback, the service refuses to start (exit `2`) without a
verifier unless you opt out by name with
`services.rpc.insecure_remote`, and without a `--policy` it enforces
`kit-default` unless you opt out with `services.rpc.insecure_no_policy`.
The permission gate (`cli.WithPermission`), the audit sinks
(`cli.WithAuditSinks`) and the walkthrough are shared with the other
services: [secure-remote-serving.md](secure-remote-serving.md).

### 11. Add your own interceptors

`rpcserve.Config.Interceptors` takes Connect interceptors for your
own concerns, such as metering against your own billing. Rate limits,
quotas and tracing are built in (step 10). They run inside kit's
gates, so they see only calls kit admitted:

```go
rpcserve.With(rpcserve.Config{
    Auth:         verify,
    Interceptors: []connect.Interceptor{meter},
})
```

A call that authentication, `kit/auth-required`, confirmation,
`Expose`, the destructive ceiling, the permission gate or a limit
refuses never reaches them.
A call they refuse does not run; the client gets their error and the
audit trail records it. They apply in order, the first outermost.

The request they see is the body as the client sent it, so
`req.Msg.Meta.Caller` is a claim. Read the caller from the verified
claims: `api.IdentityOf(rpc.ClaimsFromContext(ctx))`.

## Refusals

A refusal is an RPC error, and the command never ran:

| Code                  | When                                                         |
|-----------------------|--------------------------------------------------------------|
| `not_found`           | unknown command, hidden by `Expose`/`Hide`, or never remote  |
| `unauthenticated`     | the verifier refused the call, or `kit/auth-required` without a verified caller |
| `failed_precondition` | `kit/requires-confirmation` without `X-Confirm-Token`        |
| `permission_denied`   | destructive ceiling, a scope the command's `kit/permissions` names that `Auth`'s claims lack (message led by `cmdsurface: insufficient scope`), `--policy`, or `cli.WithPermission`; the message says which. Also the listener's Host and Origin checks, the message led by `host_rejected` or `origin_rejected` |
| `resource_exhausted`  | request over the body limit (`services.rpc.body_limit.max_bytes`, else `MaxBodyBytes`), or the caller is over its rate limit or quota (with `Retry-After` metadata) |
| `unavailable`         | every in-flight slot and the queue are full (`concurrency`); retry after `Retry-After` |
| `deadline_exceeded`   | the command ran past its deadline (`kit/timeout`, else `timeouts.command`) |
| `aborted`             | the same idempotency key is still running |
| `invalid_argument`    | an idempotency key reused for a different call |

## Option reference

| Option | Default | Effect |
|---|---|---|
| `Config.Addr` | `127.0.0.1:8082` | Listen address. `services.rpc.addr`, then `--rpc-addr`, override it. |
| `Config.Auth` | none | Authenticates every call; permits a non-loopback address. |
| `Config.InsecureRemote` | `false` | Serve unauthenticated beyond loopback. `services.rpc.insecure_remote` sets the same. |
| `Config.InsecureNoPolicy` | `false` | Beyond loopback with no `--policy`, serve with no policy instead of `kit-default`. `services.rpc.insecure_no_policy` sets the same. |
| `Config.Policy` | zero | Zero refuses every destructive command. `AllowDestructiveOn: [SurfaceRPC]` permits them. |
| `Config.Expose` | empty | Empty reaches the whole tree; a non-empty list is an allow-list. |
| `Config.Hide` | empty | Patterns withheld from RPC, applied after `Expose`. |
| `Config.MaxBodyBytes` | 4 MiB | Largest request; larger is `resource_exhausted`. `services.rpc.body_limit.max_bytes` / `.enabled`, then `services.all.body_limit.*`, override it. |
| `Config.Interceptors` | none | Your Connect interceptors, run only for calls kit admitted. |

## What the service does not implement

- **Server reflection.** Clients are given `commands.proto`.
- **TLS by default.** The listener is plaintext, HTTP/1.1 and h2c,
  until `services.rpc.tls` names a certificate; client certificates
  authenticate with `services.rpc.auth.mode: mtls`. See
  [secure-remote-serving.md](secure-remote-serving.md#10-encrypt-the-connection-at-a-proxy-or-on-the-listener).
- **CORS, yet.** A browser client on another origin needs a proxy
  that answers CORS; see [step 5](#5-call-it-from-a-browser-over-grpc-web).
- **A procedure per command.** Commands are addressed by path inside
  one service, so the schema never changes when your tree does.
- **Interactive and self-hosting commands.** They need a terminal or
  would replace the process serving the call.

## Related pages

- [rpc README](../../../go/transport/rpc/README.md): the server, the
  interceptors, h2c
- [rpcserve README](../../../go/console/cli/rpcserve/README.md): the
  service at a glance
- [cmdsurface reference §RPC](../reference/cmdsurface.md#rpc):
  `MountRPC`, the wire mapping, mounting the service by hand
- [secure-remote-serving.md](secure-remote-serving.md): auth beyond
  loopback, the permission gate, the audit trail
- [serve-lifecycle contract §"The rpc service"](../../contracts/serve-lifecycle.md#the-rpc-service):
  the normative text
