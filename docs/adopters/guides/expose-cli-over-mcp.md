# Expose your CLI over MCP

Serve your command tree as MCP tools with kit's built-in `mcp`
service: one tool per command, over streamable HTTP or over stdio for
a host that spawns your tool, behind the same gates as REST and the
socket.

## Who this is for

Developers with a kit CLI who want LLM hosts — Claude Desktop, Claude
Code, IDE agents, gateway-fronted fleets — to call their commands as
MCP tools. For the *static* tool descriptors `<tool> spec --format mcp`
renders, see the
[toolspec adopter guide](../integrations/toolspec-adopter-guide.md)
instead — that path never executes anything. It publishes the same
tool shape this service serves (one tool per command, same names, same
`inputSchema`), so a client can discover tools statically and call
them here; a parity test pins the two together.

Serving a bare cobra tree without a kit root, or wanting the SDK
server in your own hands? See
[serve-mcp-with-the-sdk.md](serve-mcp-with-the-sdk.md). Already
calling `cmdsurface.MountMCP`? It is deprecated; see
[Move off MountMCP](#move-off-mountmcp).

## Before you begin

You need:

- A kit root built with `cli.New` (see
  [create-cli-project.md](create-cli-project.md)). A project generated
  by `kit init --from cli-go` already registers the service: skip to
  [step 2](#2-serve-it).
- Safety annotations on your commands — `kit/side-effect`,
  `kit/auth-required`, `kit/requires-confirmation`. The gates below
  read them.
- Room for the SDK. Serving MCP links the official MCP Go SDK: about
  2 MB on a stripped build (`-ldflags "-s -w"`) and about 3 MB with
  symbols, measured on the cli-go scaffold for linux/amd64 and
  darwin/arm64. A tool that does not register the service does not
  link the SDK.

## Steps

### 1. Register the service

```go
package main

import (
    "context"
    "os"

    "hop.top/kit/go/console/cli"
    "hop.top/kit/go/console/cli/mcpserve"
)

func main() {
    root := cli.New(cli.Config{Name: "mytool", Version: "1.4.2", Short: "Manage widgets"},
        cli.WithStatus(cli.StatusConfig{}),
        cli.WithAPI(cli.APIConfig{}),
        mcpserve.With(mcpserve.Config{}),
    )
    root.Cmd.AddCommand(widgetCmd())
    if err := root.Execute(context.Background()); err != nil {
        os.Exit(1)
    }
}
```

`mcpserve.With` registers a service named `mcp`. Like `socket`, it is
registered and not enabled: a bare `mytool serve` leaves it off,
`mytool serve mcp` starts it, and `services.mcp.enabled: true` puts it
under a bare `serve`. It does not need the `api` service; register it
alone if MCP is all you serve.

Every command the REST projection would mount becomes one tool, named
by its path joined with dots (`widget add` → `widget.add`). Its
`inputSchema` lists the command's flags by long name and, when the
command declares its positional arguments, one `args` array — see
[Positional arguments](#positional-arguments).

### 2. Serve it

Over streamable HTTP, on the service's own listener:

```console
$ mytool serve mcp
INFO serve: ready_reported object=service elapsed_ms=0 service=mcp address=http://127.0.0.1:8081/mcp
```

The listener is the service's own, not the api's: `serve mcp` runs
without the api, and stopping one never stops the other. Wait for the
`ready_reported` line and read the endpoint from `address=`.

Over stdio, for a host that spawns the tool:

```sh
mytool serve mcp --stdio
```

Standard output then carries protocol messages only; logs, the
lifecycle trace and hints go to standard error. When the host closes
standard input the service answers every request it has already read,
then stops, and the process exits `0`.

Both surfaces under one supervisor: `mytool serve --enable mcp` runs
`api` on `127.0.0.1:8080` and `mcp` on `127.0.0.1:8081`.

### 3. Connect a host

Hosts that spawn MCP servers read an `mcpServers` map —
`claude_desktop_config.json` for Claude Desktop, `.mcp.json` at the
project root for Claude Code, `mcp.json` for Cursor:

```json
{
  "mcpServers": {
    "mytool": {
      "command": "/usr/local/bin/mytool",
      "args": ["serve", "mcp", "--stdio"]
    }
  }
}
```

Give `command` an absolute path: a desktop app does not inherit your
shell's `PATH`. A host that connects over HTTP takes the `address=`
from the `ready_reported` line instead.

## Verify the result

Streamable HTTP wants both media types in `Accept`, and the
`initialize` answer names the session every later request carries:

```bash
curl -si http://127.0.0.1:8081/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}'
```

```text
HTTP/1.1 200 OK
Content-Type: text/event-stream
Mcp-Session-Id: SBRE7GW3BZQNZXVRSF6OQEIQJ6

event: message
data: {"jsonrpc":"2.0","id":1,"result":{"capabilities":{"logging":{},"tools":{"listChanged":true}},"protocolVersion":"2025-06-18","serverInfo":{"name":"mytool","version":"1.4.2"}}}
```

Send `notifications/initialized`, then `tools/list` and `tools/call`
with the `Mcp-Session-Id` and `MCP-Protocol-Version` headers. A read
command that declares an output schema (`cli.SetOutputSchema`)
answers in `structuredContent`, with an empty text block beside it:

```text
event: message
data: {"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":""}],"structuredContent":[{"name":"bolt"},{"name":"nut"}]}}
```

Over stdio, pipe the requests in; each is answered on standard output
before the process exits `0`:

```bash
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"sh","version":"0"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' |
  mytool serve mcp --stdio
```

In practice, point an MCP client at the endpoint rather than curl: the
[MCP Inspector](https://github.com/modelcontextprotocol/inspector)
connects to either transport.

## What the service enforces

The tool list is what may run and nothing else. The service withholds
exactly what the REST projection withholds at mount — interactive,
management-only (kit's reserved verbs such as `status`) and
self-hosting commands, a destructive command `Policy` does not permit
on `mcp`, and a command the permission gate refuses for every caller.
A call naming a withheld tool is an unknown tool. A client whose
identity the service established — verified by `Config.Auth` over
HTTP, the spawning peer over stdio — also gets its own list: a tool
the permission gate would refuse it (a missing `kit/permissions` scope,
a `--policy` caller rule, your `cli.WithPermission` decision) is left
off. Listing is advisory; every call still passes the bridge's gates.

| Gate | Over HTTP | Over stdio |
|------|-----------|------------|
| Exposure | loopback by default; a non-loopback address needs `Config.Auth` or `services.mcp.insecure_remote` (refused at exit `2` otherwise), and with no `--policy` enforces `kit-default` unless `services.mcp.insecure_no_policy` | no address, no rule |
| `kit/auth-required` | runs only when `Config.Auth` verified the request; a bare `Authorization` header is not authentication | runs: the peer spawned the process and already holds your user's authority |
| `kit/requires-confirmation` | an elicitation the client's user accepts, or an `X-Confirm-Token` header | an elicitation the client's user accepts |
| destructive | withheld until `Policy.AllowDestructiveOn` names `cmdsurface.SurfaceMCP`; then the command's own `confirm` argument | same |
| permission, audit | `cli.WithPermission`, `cli.WithAuditSinks` | same |

A refusal is an `isError` tool result, never an HTTP status:
`authentication required`, `confirmation required` (naming both
remedies), or `confirmation declined`. The confirmation question is
asked only after every machine gate has admitted the call, so a
caller a machine gate refuses never sees a prompt, and an accepted
answer lifts nothing but that one gate.

The normative text is the
[serve-lifecycle contract, "The mcp service"](../../contracts/serve-lifecycle.md#the-mcp-service).

## Configure it

| Key | Default | Meaning |
|-----|---------|---------|
| `services.mcp.enabled` | `false` | start under a bare `serve` |
| `services.mcp.transport` | `http` | `http` or `stdio`; `--stdio` wins for one run |
| `services.mcp.addr` | `127.0.0.1:8081` | HTTP listen address; `--mcp-addr` wins for one run |
| `services.mcp.path` | `/mcp` | HTTP endpoint path |
| `services.mcp.insecure_remote` | `false` | serve HTTP unauthenticated beyond loopback |
| `services.mcp.insecure_no_policy` | `false` | beyond loopback with no `--policy`, serve HTTP with no policy instead of `kit-default` |

The two opt-ins have no flags — `--insecure-remote` and
`--insecure-no-policy` name the api service — so a reviewer finds
every such deployment by key. `--mcp-addr` with the stdio transport is
refused at exit `2`: it would name an address nothing listens on.

`mcpserve.Config` holds the code defaults under the same names, plus
what only code can say:

```go
mcpserve.With(mcpserve.Config{
    // Verify every HTTP request; admits kit/auth-required commands.
    Auth: verifyBearer,
    // Permit destructive commands over MCP; each still needs its
    // own confirm argument.
    Policy: cmdsurface.Policy{
        AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceMCP},
    },
    // Keep admin commands off the tool list.
    Hide: []string{"admin *"},
    // Offered to clients at initialization.
    Instructions: "Widget inventory. Read before you write.",
})
```

`Auth` is an `api.AuthFunc`, the type `APIConfig.Auth` takes;
[secure-remote-serving.md](secure-remote-serving.md) walks it.

### Let MCP clients sign in (OAuth)

An MCP client that speaks the MCP authorization spec finds your
authorization server by itself when the service names it. Configure
the provider and the endpoint's own URL as the audience:

```yaml
services:
  mcp:
    addr: 0.0.0.0:8081
    auth:
      mode: oidc
      oidc:
        issuer: https://login.example.com/
        audience: https://mcp.example.com/mcp   # the endpoint URL clients use
```

A request without a valid token is answered `401` with
`WWW-Authenticate: Bearer resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource/mcp"`,
and that document (RFC 9728) names the resource and the issuer, so the
client runs the sign-in and retries with a token issued for this
endpoint. A token for any other audience is refused. `jwks` and `jwt`
do the same when their block names an `issuer`; with no issuer or no
URL audience there is no document, and the challenge is plain
`Bearer`. stdio is unchanged: the spawning process is the trust.

Mounting the surface yourself, `mcpsdk.WithProtectedResource(pr, fn)`
does the same in front of `mcpsdk.Mount` or `Handler`, with `pr` from
`api.NewProtectedResource` and `fn` a verifier from
`go/transport/authn`; the caller it verifies becomes each call's
identity. The deprecated `MountMCP` takes
`cmdsurface.WithMCPProtectedResource` with the same arguments.

### Positional arguments

A tool can only carry the positional arguments its command declares.
Name them, in order, in the `kit/args` annotation; a trailing `?`
marks one optional:

```go
cmd := &cobra.Command{
    Use:         "tag <name> [note]",
    Args:        cobra.RangeArgs(1, 2),
    Annotations: map[string]string{"kit/args": "name,note?"},
    // ...
}
```

The tool's `inputSchema` then gains one `args` property beside the
flags — the key and shape REST and the socket already use:

```json
"args": {
  "type": "array",
  "items": {"type": "string"},
  "description": "Positional arguments in order: name, note?",
  "minItems": 1
}
```

`args` is listed in `required` when any argument is required. A
caller sends `{"args": ["bolt", "spare"], "force": true}`; a missing
required argument or an `args` that is not an array of strings comes
back as an `isError` result naming the problem, before any
confirmation prompt.

A command whose usage line names operands (`label <name>`) without
declaring them in `kit/args` publishes no `args` property, and its
tool description says the arguments cannot be passed; declare them to
make the tool callable. The same note appears when a flag of the
command is itself named `args`: the flag keeps the property.

### Prompts, resources, and the rest of the SDK

`Config.ServerOptions` takes `mcpsdk` options and hands them to the
SDK surface before the service's own, so identity, provenance and the
gates always win. `mcpsdk.WithServerConfigurator` registers prompts,
resources and templates on the raw `*mcp.Server`; what you register
there runs outside kit's gates —
[serve-mcp-with-the-sdk.md](serve-mcp-with-the-sdk.md#beyond-tools-the-rest-of-the-sdk)
covers the options and the trust boundary.

### Protocol versions

Over HTTP, a client that runs the `initialize` handshake gets a
stateful session at a protocol version through `2025-11-25`; a
`2026-07-28` request, which carries its version per request, is
answered statelessly on the same endpoint, routed by the markers in
[Routing precedence](#routing-precedence). The stdio transport serves
`2026-07-28` as well.

## Move off MountMCP

`cmdsurface.MountMCP`, its `MCPOption` / `WithMCP*` options and the
`mcp:` config block are deprecated: frozen, fixes only, with removal
no earlier than kit 0.6.0 and only once the replacements cover what
the mount still does — the
[cmdsurface reference's Status](../reference/cmdsurface.md#status)
holds the schedule. On a kit root the mcp service replaces it; on a
bare bridge, [`mcpsdk`](serve-mcp-with-the-sdk.md) does.

| With `MountMCP` | With the mcp service |
|-----------------|----------------------|
| `cmdsurface.New(root)` + a router + `http.ListenAndServe` | `mcpserve.With(mcpserve.Config{})`; the service owns the listener and lifecycle |
| a bridge built in `APIConfig.Handlers` to share the api's port | its own listener, `services.mcp.addr` |
| `WithMCPPath` | `services.mcp.path` / `Config.Path` |
| `WithMCPServerInfo` | the root's `Name` and `Version` |
| `b.Expose` / `b.Hide` / `WithPolicy` on the bridge | `Config.Expose` / `Hide` / `Policy` |
| an `api.Auth` on the router verifying auth-required calls | `Config.Auth` verification over HTTP; spawn trust over stdio |
| `WithMCPConfirmationKey` (MRTR) | built in: the service asks through elicitation, keyed per process |
| `WithMCPSpecVersions` | negotiated by the SDK — see [Protocol versions](#protocol-versions) |
| the `mcp:` config block | `services.mcp.*` |
| no stdio | `--stdio` |

What does not carry over: gate refusals mirrored as HTTP `401` /
`428` (the SDK reports `isError` only), the zero-dependency build, the
`ttlMs` / `cacheScope` cache hints, and `WithMCPOriginAllowlist` (the
service's listener runs kit's Host and Origin checks instead, set by
`services.mcp.host_check` and `services.mcp.origin_check`; the SDK's
DNS-rebinding check stays on beneath them for loopback names).

## The deprecated `MountMCP` mount

Reference for existing callers until removal.

### What it serves

`MountMCP` serves **two MCP protocol revisions from one mount**:

- **2024-11-05** — the `initialize` handshake era. Plain JSON-RPC
  over POST; `initialize`, `tools/list`, `tools/call`.
- **2026-07-28** — the stateless era. No handshake, no sessions;
  every request carries its protocol version and client capabilities
  in `params._meta`; adds `server/discover`, cacheable list results,
  and mid-call confirmation round-trips (MRTR).

Every incoming POST is routed to exactly one revision's handler by
per-request detection (below). Both revisions expose the same tools,
run through the same safety policy, and dispatch through the same
bridge — there is no way to reach a command on one revision that the
other would have blocked.

### Mount it

```go
package main

import (
    "log"
    "net/http"
    "time"

    "hop.top/kit/go/transport/api"
    "hop.top/kit/go/transport/cmdsurface"
)

func main() {
    root := buildCobraTree() // your existing CLI root

    b := cmdsurface.New(root)

    r := api.NewRouter()
    if err := cmdsurface.MountMCP(b, r,
        cmdsurface.WithMCPServerInfo("mytool", "1.4.2"),
        cmdsurface.WithMCPCacheHints(30*time.Second, cmdsurface.MCPCacheScopePrivate),
        cmdsurface.WithMCPOriginAllowlist("https://app.example.com"),
    ); err != nil {
        log.Fatal(err)
    }

    log.Fatal(http.ListenAndServe("127.0.0.1:8080", r))
}
```

Every leaf becomes one MCP tool named by its dotted path
(`widget add` → `widget.add`), with an `inputSchema` derived from
its pflag set and its declared positional arguments
([Positional arguments](#positional-arguments) applies unchanged).
MCP is in the default enablement set (`DefaultPolicy()` enables
`cli`, `lib`, `mcp`), so no `Expose` call is needed unless you've
narrowed enablement.

Options are validated at mount time — an unrecognized spec version,
a negative cache TTL, an unknown cache scope, or an explicitly empty
confirmation key makes `MountMCP` return an error instead of
mounting a half-configured surface.

### Verify the legacy path

A 2024-11-05 client needs nothing special:

```bash
curl -s http://127.0.0.1:8080/mcp \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize"}'
```

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": "2024-11-05",
    "capabilities": {"tools": {}},
    "serverInfo": {"name": "mytool", "version": "1.4.2"}
  }
}
```

`tools/list` and `tools/call` work the same way — plain JSON-RPC
bodies, no extra headers.

### Verify the modern path

A 2026-07-28 request is stricter: two reserved `_meta` keys in the
body and matching HTTP headers (`Mcp-Name` additionally on
`tools/call`). Discovery first:

```bash
curl -s http://127.0.0.1:8080/mcp \
  -H 'Content-Type: application/json' \
  -H 'MCP-Protocol-Version: 2026-07-28' \
  -H 'Mcp-Method: server/discover' \
  -d '{
    "jsonrpc": "2.0", "id": 2, "method": "server/discover",
    "params": {"_meta": {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28",
      "io.modelcontextprotocol/clientCapabilities": {}
    }}
  }'
```

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "resultType": "complete",
    "supportedVersions": ["2026-07-28"],
    "capabilities": {"tools": {}},
    "ttlMs": 0,
    "cacheScope": "private",
    "_meta": {
      "io.modelcontextprotocol/serverInfo": {
        "name": "mytool", "version": "1.4.2"
      }
    }
  }
}
```

Then a call — note the third header:

```bash
curl -s http://127.0.0.1:8080/mcp \
  -H 'Content-Type: application/json' \
  -H 'MCP-Protocol-Version: 2026-07-28' \
  -H 'Mcp-Method: tools/call' \
  -H 'Mcp-Name: widget.add' \
  -d '{
    "jsonrpc": "2.0", "id": 3, "method": "tools/call",
    "params": {
      "name": "widget.add",
      "arguments": {"name": "foo"},
      "_meta": {
        "io.modelcontextprotocol/protocolVersion": "2026-07-28",
        "io.modelcontextprotocol/clientCapabilities": {}
      }
    }
  }'
```

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "result": {
    "resultType": "complete",
    "content": [{"type": "text", "text": "added widget foo\n"}],
    "isError": false,
    "_meta": {
      "io.modelcontextprotocol/serverInfo": {
        "name": "mytool", "version": "1.4.2"
      }
    }
  }
}
```

The headers are not optional decoration: the server validates that
`MCP-Protocol-Version` matches the `_meta` protocol version,
`Mcp-Method` matches the body `method`, and `Mcp-Name` matches
`params.name`, so gateways can route and authorize on headers alone
without a body parse. Any absence or disagreement is rejected with
JSON-RPC error `-32020` at HTTP 400; an unsupported version gets
`-32022` with the supported list in `error.data`. (`-32021` exists
in the spec for missing client capabilities; kit requires none, so
it is never sent.)

`Result.Stdout` becomes a text content block; `Result.Stderr` adds
a `[stderr]&#32;`-prefixed block; structured `Result.Data` is emitted
both as a JSON text block and as `structuredContent`. A non-zero
exit code sets `isError: true`.

### Configure from YAML

The `mcp:` config block, deprecated with the mount, is the
declarative counterpart of its options:

```yaml
mcp:
  spec_versions: ["2024-11-05", "2026-07-28"]  # empty = both
  path: /mcp                                   # empty = "/mcp"
  cache_ttl_ms: 30000                          # 0 = immediately stale
  cache_scope: private                         # "" = private
  origin_allowlist: ["https://app.example.com"]
```

The block is **declarative only**: `Load` / `LoadFile` parse it, but
`FromConfig` does not mount surfaces (same posture as the webhook,
bus, and cron blocks). You read `cfg.MCP` and translate it to
options yourself:

```go
func mcpOptions(cfg *cmdsurface.MCPConfig) []cmdsurface.MCPOption {
    if cfg == nil {
        return nil
    }
    var opts []cmdsurface.MCPOption
    if len(cfg.SpecVersions) > 0 {
        versions := make([]cmdsurface.MCPSpecVersion, len(cfg.SpecVersions))
        for i, v := range cfg.SpecVersions {
            versions[i] = cmdsurface.MCPSpecVersion(v)
        }
        opts = append(opts, cmdsurface.WithMCPSpecVersions(versions...))
    }
    if cfg.Path != "" {
        opts = append(opts, cmdsurface.WithMCPPath(cfg.Path))
    }
    if cfg.CacheTTLMs != 0 || cfg.CacheScope != "" {
        scope := cmdsurface.MCPCacheScope(cfg.CacheScope)
        if scope == "" {
            scope = cmdsurface.MCPCacheScopePrivate
        }
        opts = append(opts, cmdsurface.WithMCPCacheHints(
            time.Duration(cfg.CacheTTLMs)*time.Millisecond, scope))
    }
    if len(cfg.OriginAllowlist) > 0 {
        opts = append(opts, cmdsurface.WithMCPOriginAllowlist(cfg.OriginAllowlist...))
    }
    return opts
}
```

### Option reference

| Option | Default | Effect |
|---|---|---|
| `WithMCPPath(path)` | `/mcp` | Mount path. |
| `WithMCPServerInfo(name, version)` | `cmdsurface` / `0.0.0` | Identity in the legacy `initialize` result and in every modern result's `_meta` serverInfo. |
| `WithMCPSpecVersions(versions...)` | both | Enabled revision set (`MCPSpec20241105`, `MCPSpec20260728`). Duplicates dedupe; an empty call or unknown version fails the mount. |
| `WithMCPCacheHints(ttl, scope)` | `0` / `private` | `ttlMs` + `cacheScope` on modern `server/discover` and `tools/list` results. |
| `WithMCPOriginAllowlist(origins...)` | no check | Exact-match `Origin` validation on the modern path; mismatch → HTTP 403. |
| `WithMCPConfirmationKey(key)` | header gate | Enables the MRTR confirmation round-trip for confirmation-gated leaves (below). Key must be non-empty and shared across instances. |

### How version detection works

You never pick a version per request — the mount does, from the
request itself:

| Request looks like | Served as |
|---|---|
| `method: "initialize"` (with or without modern markers) | 2024-11-05 |
| `Mcp-Method` or `Mcp-Name` header present, `params._meta` carries the reserved `io.modelcontextprotocol/protocolVersion` key, or `method: "server/discover"` | 2026-07-28 |
| anything else | 2024-11-05, byte-for-byte today's behavior |

Two things deliberately do **not** route a request modern: a bare
`params._meta` (legacy clients legitimately send
`_meta.progressToken` and OTel keys) and the
`MCP-Protocol-Version` *header* alone (SDK clients that negotiated
2024-11-05 through the handshake send it on every subsequent
request; on the legacy path it is ignored). A malformed
modern-looking request is answered with modern spec errors, never
silently demoted to legacy.

When the modern revision is enabled, GET and DELETE at the mount
path answer HTTP 405, and the `tasks/*` extension methods answer
`-32601` (method not found) — kit does not implement the tasks
extension and, per spec, advertises no `extensions` map in
`server/discover`, which *is* the conformant way to not support it.

Pinning one revision: `WithMCPSpecVersions(MCPSpec20241105)` mounts
today's handler alone (markers ignored, exactly as before this
feature); `WithMCPSpecVersions(MCPSpec20260728)` serves every
request modern — legacy `initialize` then fails validation with an
error message naming the supported version, which is the correct
signal for a legacy client with no fall-forward mechanism.

#### Routing precedence

The first rule that applies wins:

| Rule | Condition | Route |
|---|---|---|
| D1 | body unreadable or not JSON | answered by the mount itself: `-32603` / `-32700` at HTTP 400, identical to the legacy responses, whatever the headers |
| D2 | `method: "initialize"` | legacy, even with modern markers present |
| D3 | any modern marker (M1 `Mcp-Method` header, M2 `Mcp-Name` header, M3 reserved `_meta` protocolVersion key, M4 `server/discover`) | modern; an incomplete or contradictory modern request is rejected with modern errors, never demoted |
| D4 | no marker | legacy, byte-for-byte |

With only one revision enabled the rules collapse: legacy-only mounts
the legacy handler directly (markers ignored); modern-only sends every
request through the modern validation below, and a rejected
`initialize` names the supported version in its error message.

Edge cases, both revisions enabled:

| Request | Route | Response |
|---|---|---|
| `initialize`, with or without markers | legacy | 2024-11-05 initialize result |
| `tools/list` / `tools/call` with only an `MCP-Protocol-Version: 2024-11-05` header | legacy | legacy response; header ignored |
| unknown method, no markers | legacy | `-32601` at HTTP 200 |
| `tools/call` with the `_meta` protocolVersion key only | modern | `-32602` at 400 (`clientCapabilities` missing) |
| `tools/call` with complete `_meta`, no headers | modern | `-32020` at 400 (`MCP-Protocol-Version` header missing) |
| `tools/call` with `Mcp-Method` header only | modern | `-32602` at 400 (required `_meta` missing) |
| bare `server/discover` | modern | `-32602` at 400 |
| unknown method in a valid modern envelope | modern | `-32601` at HTTP 404 |
| notification (no `id`) with markers | modern | HTTP 202, empty body, not processed |
| `id: null` with markers | modern | `-32600` at 400 |

#### Modern validation order

A request routed modern is checked in this order; the first failure
responds and stops:

| Check | Rule | Failure |
|---|---|---|
| V1 | `jsonrpc` absent or `"2.0"` | `-32600` at 400 |
| V2 | `id` absent → notification (202, discarded); present `id` must be a string or an integer — `null`, boolean, float, object and array are rejected | `-32600` at 400 |
| V3 | `params._meta` carries `io.modelcontextprotocol/protocolVersion` and `io.modelcontextprotocol/clientCapabilities` (`clientInfo` optional) | `-32602` at 400 |
| V4 | `MCP-Protocol-Version` header present and equal to the `_meta` protocolVersion | `-32020` at 400 |
| V5 | protocolVersion is `2026-07-28` | `-32022` at 400, `data: {"supported": ["2026-07-28"], "requested": ...}` |
| V6 | `Mcp-Method` header present and equal to body `method` | `-32020` at 400 |
| V7 | `tools/call` only: `Mcp-Name` header present, non-empty after Base64-sentinel (`=?base64?...?=`) decoding, and byte-equal to `params.name`, which must be present | `-32020` at 400 |
| V8 | method is `server/discover`, `tools/list` or `tools/call` | `-32601` at 404 |
| V9 | per-method params (e.g. unknown tool name) | `-32602` at 200 |

V7 runs before params decoding, so a `tools/call` without `Mcp-Name`
gets `-32020` even when `params` is malformed. A routing header sent
twice with identical values counts once; sent twice with different
values it fails its check with `-32020`, because gateways and the
server could otherwise act on different values. `-32022`'s `supported`
list omits 2024-11-05: that revision is reachable only through its
handshake. Inbound `Mcp-Param-*`, `Mcp-Session-Id` and `Last-Event-ID`
headers are ignored.

### Destructive commands and confirmation

Safety annotations gate the MCP surface exactly like every other
remote surface, on both revisions:

- **`kit/side-effect=destructive`** leaves are blocked unless
  `SurfaceMCP` is in `Policy.AllowDestructiveOn`. A blocked call is
  an `isError` result, not an execution. No confirmation outcome
  ever relaxes this ceiling.
- **`kit/auth-required=true`** leaves run only for a request an
  `api.Auth` on the router verified — refused with an `isError`
  result at HTTP 401 (with `WWW-Authenticate`) otherwise. An
  `Authorization` header's presence is not verification.
- **`kit/requires-confirmation=true`** leaves require the
  `X-Confirm-Token` header — refused with an `isError` result at
  HTTP 428 otherwise. This header gate is the default on both
  revisions.

#### MRTR confirmation (2026-07-28 opt-in)

The modern revision can replace the confirmation header with the
spec-native in-band round-trip. Provisioning key material is
**required** to enable it — there is no generated default, because
the key must verify state across instances:

```go
_ = cmdsurface.MountMCP(b, r,
    cmdsurface.WithMCPConfirmationKey(key)) // non-empty; same key on every instance
```

With a key configured, a client that declares the `elicitation`
capability in `_meta` and calls a confirmation-gated tool receives
`resultType: "input_required"` instead of an execution:

```json
{
  "resultType": "input_required",
  "inputRequests": {
    "confirm": {
      "method": "elicitation/create",
      "params": {
        "mode": "form",
        "message": "Approve execution of \"widget.purge\"?",
        "requestedSchema": {"type": "object", "properties": {}}
      }
    }
  },
  "requestState": "v1.<expiry>.<mac>"
}
```

The client asks its user, then retries the call with the echoed
state and the answer:

```json
{
  "name": "widget.purge",
  "requestState": "v1.<expiry>.<mac>",
  "inputResponses": {"confirm": {"action": "accept"}},
  "_meta": { "...": "as before" }
}
```

`accept` runs the leaf; `decline` / `cancel` refuse it. The state
is HMAC-SHA-256-protected and bound to the tool, its arguments, and
the caller's `Authorization` value, with a five-minute expiry.
Expiry is a routine re-prompt; a state that fails verification is
never honored — it is recorded as a security-relevant audit event
on the bridge's registered sinks, then re-prompted with fresh
state. Clients that don't declare `elicitation` keep the
`X-Confirm-Token` header gate even when a key is configured.

### Origin validation — configure it

The MCP spec requires servers to validate the `Origin` header
(DNS-rebinding defense). Kit cannot know which origins are valid
for your deployment, so the check is opt-in — **which means an
unconfigured mount performs no Origin check at all**. Do one of:

- serve the mount on localhost only (as the example above does), or
- pass `WithMCPOriginAllowlist(...)` with the exact origins your
  clients send, or
- terminate at an authenticating proxy that owns Origin policy.

If none of those hold — a mount bound to a routable interface, no
allowlist, no proxy — any web page a browser on the network visits
can hit your tools. Configure the allowlist.

Requests without an `Origin` header (curl, server-to-server) are
never refused by the allowlist; a present-but-unlisted Origin gets
HTTP 403.

### Cache hints

Modern `server/discover` and `tools/list` results carry `ttlMs` and
`cacheScope` so clients and gateways can cache them. The defaults
are deliberately conservative: `ttlMs: 0` (immediately stale —
`Expose` / `Hide` can change the tool list at runtime and there is
no change notification) and `cacheScope: "private"`. If your tool
list is stable and identical for every caller, opt in:

```go
cmdsurface.WithMCPCacheHints(5*time.Minute, cmdsurface.MCPCacheScopePublic)
```

`tools/call` results are never cacheable and carry no hints.

### Auth posture

The surface itself is **auth-scheme-agnostic**: it verifies nothing,
and admits a `kit/auth-required` leaf only for a request an `api.Auth`
middleware on the router verified, whatever scheme that verifier
speaks. The 2026-07-28 authorization hardening lives where each
obligation belongs — kit never implemented client registration, token
issuance, or an authorization server:

- **RFC 9207 issuer validation** — the client half ships in kit on
  the OAuth *callback* surface: set `OAuthProvider.ExpectedIssuer`
  and callbacks reject responses whose `iss` is missing or wrong.
- **CIMD vs. DCR, `application_type`, credential binding** — these
  bind the authorization server / resource server deployed in front
  of the mount. Choose them there; existing DCR-based deployments
  keep working.

Full deployment guidance:
[cmdsurface ADOPTER_GUIDE](../../../go/transport/cmdsurface/ADOPTER_GUIDE.md).

### What the surface does not implement

Absence is spec-conformant — capabilities not advertised are
capabilities not supported:

- `prompts/*`, `resources/*`, subscriptions, list-changed
  notifications, SSE response streams (responses are always single
  JSON objects).
- `tools/list` pagination — a `cursor` param is ignored, no
  `nextCursor`.
- The `io.modelcontextprotocol/tasks` extension — `tasks/*` methods
  answer `-32601`, and `server/discover` advertises no extensions.
- Optional 2026-07-28 tool-descriptor fields (`title`, `icons`,
  `outputSchema`, `annotations`).

## Related pages

- [serve-lifecycle contract, "The mcp service"](../../contracts/serve-lifecycle.md#the-mcp-service)
  — the normative rules for the service
- [`go/console/cli/mcpserve`](../../../go/console/cli/mcpserve/README.md)
  — the package
- [serve-mcp-with-the-sdk.md](serve-mcp-with-the-sdk.md) — the SDK
  surface on a bare bridge, prompts, resources, the trust boundary
- [secure-remote-serving.md](secure-remote-serving.md) — `Auth`, the
  permission gate, the audit trail
- [toolspec adopter guide](../integrations/toolspec-adopter-guide.md)
  — static MCP descriptors (`<tool> spec --format mcp`)
- [cmdsurface reference](../reference/cmdsurface.md) — the bridge and
  the deprecation schedule
- MCP specification:
  <https://modelcontextprotocol.io/specification/2026-07-28>
