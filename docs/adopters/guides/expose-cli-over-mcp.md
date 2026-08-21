# Expose your CLI over MCP

Mount your cobra tree as a live MCP server: one HTTP endpoint, one
MCP tool per leaf command, both current MCP protocol revisions.

## Who this is for

Developers building a kit CLI with `cmdsurface` who want LLM hosts
(Claude, IDE agents, gateway-fronted fleets) to call their commands
as MCP tools. For the *static* tool descriptor that `<tool> spec
--format mcp` renders, see the
[toolspec adopter guide](../integrations/toolspec-adopter-guide.md)
instead — that path never executes anything.

## Before you begin

You need:

- A kit project with a cobra root (see
  [create-cli-project.md](create-cli-project.md))
- `hop.top/kit/go/transport/cmdsurface` and
  `hop.top/kit/go/transport/api` importable

## What you get

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

**Nothing is deprecated.** 2024-11-05 support is preserved
byte-for-byte and has no retirement schedule. Existing `MountMCP`
calls and existing clients keep working unchanged; supporting the
new revision required no opt-in and removed nothing.

## Steps

### 1. Mount the surface

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
its pflag set. MCP is in the default enablement set
(`DefaultPolicy()` enables `cli`, `lib`, `mcp`), so no
`Expose` call is needed unless you've narrowed enablement.

Options are validated at mount time — an unrecognized spec version,
a negative cache TTL, an unknown cache scope, or an explicitly empty
confirmation key makes `MountMCP` return an error instead of
mounting a half-configured surface.

### 2. Verify the legacy path

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

### 3. Verify the modern path

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

### 4. Configure from YAML (optional)

The `mcp:` config block is the declarative counterpart of the
mount options:

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

## Option reference

| Option | Default | Effect |
|---|---|---|
| `WithMCPPath(path)` | `/mcp` | Mount path. |
| `WithMCPServerInfo(name, version)` | `cmdsurface` / `0.0.0` | Identity in the legacy `initialize` result and in every modern result's `_meta` serverInfo. |
| `WithMCPSpecVersions(versions...)` | both | Enabled revision set (`MCPSpec20241105`, `MCPSpec20260728`). Duplicates dedupe; an empty call or unknown version fails the mount. |
| `WithMCPCacheHints(ttl, scope)` | `0` / `private` | `ttlMs` + `cacheScope` on modern `server/discover` and `tools/list` results. |
| `WithMCPOriginAllowlist(origins...)` | no check | Exact-match `Origin` validation on the modern path; mismatch → HTTP 403. |
| `WithMCPConfirmationKey(key)` | header gate | Enables the MRTR confirmation round-trip for confirmation-gated leaves (below). Key must be non-empty and shared across instances. |

## How version detection works

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

Full precedence rules, worked edge cases, and rationale:
[ADR 0004](../../adr/0004-mcp-dual-spec-surface.md).

## Destructive commands and confirmation

Safety annotations gate the MCP surface exactly like every other
remote surface, on both revisions:

- **`kit/side-effect=destructive`** leaves are blocked unless
  `SurfaceMCP` is in `Policy.AllowDestructiveOn`. A blocked call is
  an `isError` result, not an execution. No confirmation outcome
  ever relaxes this ceiling.
- **`kit/auth-required=true`** leaves require an `Authorization`
  header (presence-only; scheme-agnostic) — refused with an
  `isError` result at HTTP 401 otherwise.
- **`kit/requires-confirmation=true`** leaves require the
  `X-Confirm-Token` header — refused with an `isError` result at
  HTTP 428 otherwise. This header gate is the default on both
  revisions.

### MRTR confirmation (2026-07-28 opt-in)

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

## Origin validation — configure it

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

## Cache hints

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

## Auth posture

The surface itself is **auth-scheme-agnostic**: it checks
`Authorization` presence on `kit/auth-required` leaves and nothing
else. The 2026-07-28 authorization hardening lives where each
obligation belongs, and none of it required deprecating anything in
kit — kit never implemented client registration, token issuance, or
an authorization server:

- **RFC 9207 issuer validation** — the client half ships in kit on
  the OAuth *callback* surface: set `OAuthProvider.ExpectedIssuer`
  and callbacks reject responses whose `iss` is missing or wrong.
- **CIMD vs. DCR, `application_type`, credential binding** — these
  bind the authorization server / resource server deployed in front
  of the mount. Choose them there; existing DCR-based deployments
  keep working.

Full deployment guidance:
[cmdsurface ADOPTER_GUIDE](../../../go/transport/cmdsurface/ADOPTER_GUIDE.md).

## What the surface does not implement

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

## Migration notes

Already mounting MCP? Nothing to do:

- Existing `MountMCP(b, r)` calls compile and behave unchanged;
  both revisions are enabled by default and every request a
  2024-11-05 client can send takes today's exact code path.
- New-revision clients get `server/discover`, per-request stateless
  calls, header-routable requests, cache hints, and (with a key)
  MRTR confirmations — from the same mount, against the same tools,
  under the same policy.
- The spec version each call arrived on is visible to audit sinks
  as `Meta.Extra["mcp_spec_version"]`, with client identity in
  `mcp_client_name` / `mcp_client_version` when the client sends
  `clientInfo`.
- There is no deprecation: 2024-11-05 support has no sunset date,
  and pinning to it via `WithMCPSpecVersions` remains supported.

## Related pages

- [cmdsurface README](../../../go/transport/cmdsurface/README.md) —
  full package reference, all surfaces
- [cmdsurface ADOPTER_GUIDE](../../../go/transport/cmdsurface/ADOPTER_GUIDE.md)
  — quickstart + auth hardening + confirmation key sourcing
- [ADR 0004](../../adr/0004-mcp-dual-spec-surface.md) — dual-spec
  design record: detection precedence, validation order, wire shapes
- [toolspec adopter guide](../integrations/toolspec-adopter-guide.md)
  — static MCP descriptors (`<tool> spec --format mcp`)
- MCP specification:
  <https://modelcontextprotocol.io/specification/2026-07-28>
