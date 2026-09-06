# Serve MCP from any SDK

Every kit SDK — Go, TypeScript, Python, Rust, PHP — can project its
command tree onto the Model Context Protocol from **one mount that
serves both protocol revisions**: `2024-11-05` (the `initialize`
handshake) and `2026-07-28` (the stateless per-request envelope). The
era is detected per request; you never pick one.

All five ports are byte-identical on the wire. That is not an
aspiration — it is a gate, enforced by shared fixtures on every build.

## Who this is for

You have a kit CLI in TypeScript, Python, Rust, or PHP and you want to
expose its commands as MCP tools. If you are in Go, read
[expose-cli-over-mcp.md](expose-cli-over-mcp.md) instead — it is the
reference implementation and covers the same surface in more depth.

## The 90% path

Three steps in every language:

1. Build a **bridge** over your command tree.
2. Create the **handler** with mount options.
3. Bind the handler to your HTTP stack.

Step 3 is yours. Every port exports a transport-agnostic request
handler, never a server — kit does not own your HTTP stack, and a pure
function from request to response is testable against the fixtures with
no socket open.

## Pick your language

| Language | Package | Protocol layer | Mount call |
|---|---|---|---|
| Go | `hop.top/kit` | `modelcontextprotocol/go-sdk` v1.7.0 | `cmdsurface.MountMCP` |
| PHP | `hop-top/kit` | implemented directly — see below | `new RequestHandler(...)` |
| Python | `hop-top-kit[mcp]` | `mcp` 2.0.0 + `mcp-types` 2.0.0 | `mount_mcp` |
| Rust | `hop-top-kit` feature `mcp` | `rmcp` 3.1.4 | `Surface::mount` |
| TypeScript | `@hop-top/kit/mcp` | `@modelcontextprotocol/core` + `@modelcontextprotocol/server` 2.0.0 | `createMcpHandler` |

### TypeScript

**Use the v2 scoped packages.** `@modelcontextprotocol/core` and
`@modelcontextprotocol/server`, both pinned at `2.0.0`. They are already
dependencies of `@hop-top/kit`; you do not add them yourself.

**Do not reach for `@modelcontextprotocol/sdk`.** That is the v1
package. It is legacy-era only — its `LATEST_PROTOCOL_VERSION` is
`2025-11-25` and `2026-07-28` appears nowhere in it. It cannot serve the
modern era. It is the name every search result and tutorial reaches for,
and it is the single most likely mistake on this page: code that imports
it will pass the legacy fixtures and fail every modern one.

```ts
import { createMcpHandler, commanderBridge } from '@hop-top/kit/mcp';

const bridge = commanderBridge(rootCommand, {
  run: async (inv, cmd) => ({ stdout: await execute(cmd, inv.flags), exitCode: 0 }),
});

const handler = createMcpHandler(bridge, {
  serverInfo: { name: 'my-cli', version: '1.4.0' },
});
```

`handler` is `(req: McpHttpRequest) => Promise<McpHttpResponse>`, where
both sides are `{ method, headers, body }` / `{ status, headers, body }`
with the body as raw bytes. Bind it to node:http, hono, express,
fastify, or a Worker:

```ts
// hono
app.post('/mcp', async (c) => {
  const res = await handler({
    method: 'POST',
    headers: Object.fromEntries(c.req.raw.headers),
    body: await c.req.text(),
  });
  return c.body(res.body, res.status, res.headers);
});
```

### Python

Install the extra — the MCP dependencies are optional so adopters who do
not serve MCP do not carry them:

```bash
pip install 'hop-top-kit[mcp]'
```

```python
from hop_top_kit.mcp import Bridge, Command, Result, mount_mcp

root = Command(name="app", children=[
    Command(
        name="ping",
        short="Ping the server",
        run=lambda flags: Result(stdout="pong\n"),
        annotations={"kit/side-effect": "read"},
    ),
])

app = mount_mcp(Bridge(root), server_name="my-cli", server_version="1.4.0")
```

`mount_mcp` returns an `McpSurface`. It is an ASGI callable, so mount it
under uvicorn, hypercorn, or a Starlette route directly. ASGI rather
than WSGI is deliberate: the modern era's streaming affordances are not
expressible in WSGI.

For a framework-free binding, `McpSurface.handle(request) -> Response`
is the whole contract — a pure function, no socket.

### Rust

The surface is behind the `mcp` cargo feature, so consumers who do not
serve MCP compile none of it:

```toml
[dependencies]
hop-top-kit = { version = "0.5.0-alpha.0", features = ["mcp"] }
```

```rust
use hop_top_kit::mcp::{Bridge, CallResult, HttpRequest, Leaf, MountOptions, Surface};

let bridge = Bridge::new().leaf(Leaf::new(&["ping"], "Ping the server", |_| {
    Ok(CallResult::ok("pong\n"))
}));
let surface = Surface::mount(bridge, MountOptions::default())?;

let response = surface.call(&HttpRequest::post("/mcp", body));
```

`Surface::call(&self, req: &HttpRequest) -> Response` is the
`tower::Service`-shaped function. Wire it to axum, hyper, or warp.

### PHP

**PHP implements the protocol layer directly** — it does not depend on
an official SDK, and `mcp/sdk` is deliberately absent from
`composer.json`. The released `mcp/sdk` v0.7.1 carries revisions
`2024-11-05` through `2026-01-26` and does not implement `2026-07-28`;
that revision exists only on the project's unreleased `main`. Its
`Error` type also rejects the `null` JSON-RPC id that the legacy era
must round-trip. Declaring a dependency that cannot serve half the
surface, and hand-rolling the modern era anyway, is strictly worse than
implementing the layer.

This is revisited when a release ships `2026-07-28`.

The surface is a PSR-15 `RequestHandlerInterface`. The PSR-17 factories
are injected rather than discovered, so it works in a stack with no
discovery package installed:

```php
use HopTop\Kit\Mcp\{Bridge, Mount, Policy, RequestHandler, ServerInfo};
use Nyholm\Psr7\Factory\Psr17Factory;

$factory = new Psr17Factory();

$handler = new RequestHandler(
    new Bridge($root, Policy::default()),
    new Mount(serverInfo: new ServerInfo('my-cli', '1.4.0')),
    $factory,   // PSR-17 response factory
    $factory,   // PSR-17 stream factory
);

$app->post('/mcp', $handler);   // Slim, Mezzio, Laminas
```

The full PHP walkthrough — including the php-fpm superglobal bridge and
the two PSR-7 pitfalls that break parity — is in the
[PHP SDK README](../../../sdk/experimental/php/README.md).

## Option reference

The option **set** is normative across every port; only the spelling is
idiomatic. Defaults match Go exactly.

| Go option | TS | Python | Rust | PHP | Default |
|---|---|---|---|---|---|
| `WithMCPPath` | `path` | `path` | `path` | `path` | `/mcp` |
| `WithMCPServerInfo` | `serverInfo` | `server_name` / `server_version` | `server_name` / `server_version` | `serverInfo` | `cmdsurface` / `0.0.0` |
| `WithMCPSpecVersions` | `specVersions` | `spec_versions` | `spec_versions` | `specVersions` | both eras |
| `WithMCPCacheHints` | `cacheHints` | `cache_ttl_ms` / `cache_scope` | `cache_ttl_ms` / `cache_scope` | `cacheHints` | `0` / `private` |
| `WithMCPOriginAllowlist` | `originAllowlist` | `origin_allowlist` | `origin_allowlist` | `originAllowlist` | no check |
| `WithMCPConfirmationKey` | `confirmationKey` | `confirmation_key` | `confirmation_key` | `confirmationKey` | header gate |

### Mount-time errors are refused, not absorbed

Two misconfigurations fail the mount in every port rather than starting
a server that quietly serves nothing:

- **An explicitly empty spec-version set.** Absent means "both eras";
  an empty list means "no eras", which is never what anyone wants.
- **A negative cache ttl, or an unrecognized cache scope.**

Go, TypeScript, Python, and Rust also reject an empty confirmation key.
PHP does not carry that check.

Error spelling differs by port and is not part of the contract: Go, TS,
and PHP prefix messages `cmdsurface:` and keep Go's `WithMCP*` option
names; Python and Rust prefix `mcp:` and use their own option spellings.
Match on the error *type*, never on the message text.

### Where the ports genuinely differ

Everything on the wire is identical. These are host-side API
differences, and they are real:

| | Go | TS | Python | Rust | PHP |
|---|---|---|---|---|---|
| MRTR confirmation flow | yes | **no** | yes | yes | yes |
| `tasks/*` extension | no | no | **opt-in** | opt-in | no |

**TypeScript accepts `confirmationKey` but does not act on it.** Its
modern handler checks the `X-Confirm-Token` header and nothing else, so
a confirmation-gated leaf served from TS always uses the header gate.
Do not plan an elicitation round-trip against the TS port today.

Python and Rust can serve the `tasks/*` extension when mounted
explicitly — `mount_mcp(bridge, extensions=(TasksExtension(),))` in
Python, `tasks_enabled` in Rust. Left unmounted, and in every other
port, `tasks/*` answers `-32601` and no `extensions` map is advertised.

One Rust naming trap: `mcp::Surface` is the **mounted handler**, while
the safety enum is re-exported as `mcp::SurfaceKind`. Every other port
uses `Surface` for the safety enum alone.

## Safety: `Policy.Allowed`, not `--force`

Exposure is gated by the **policy gate** — `Policy.Allowed(SafetyClass,
Surface)` in Go, and its per-language equivalents (`policyAllowed`,
`Policy.allowed`, `Policy::allowed`).

This is **not** the Factor 10 `safetyGuard` / `safety.ts` / `safety.py`
`--force` helper. That one is a CLI-time check keyed on TTY-ness, for
delegation safety. The two are unrelated, and confusing them is a real
trap: `--force` has no bearing on what a remote caller may invoke.

The rules, identical in all five ports:

1. `cli` and `lib` surfaces are always allowed (local runtime).
2. Non-destructive commands are allowed on every other surface.
3. Destructive commands are allowed only when the surface is listed in
   `allowDestructiveOn`.

**The default blocks every remote destructive invocation.** The default
policy sets `defaultEnabled = [cli, lib, mcp]` and leaves
`allowDestructiveOn` **empty** — and empty means block-all, not
allow-all. Opt a surface in explicitly:

```python
Policy(allow_destructive_on=(Surface.MCP,))
```

A blocked call comes back as an `isError` result at **HTTP 200**, not a
transport error. The call was understood and declined, not malformed —
and clients need to be able to tell those apart.

Leaves are classified from `kit/*` annotations: `kit/side-effect`
(`destructive`, `destructive-local`, `destructive-shared`),
`kit/auth-required`, and `kit/requires-confirmation`.

## Era detection: two things that look like markers and aren't

You never route by hand; the mount detects the era from the request. The
full normative rules are ported literally into every SDK, and the two
non-markers are where silent divergence hides:

- **A bare `params._meta` is not a modern marker.** `2024-11-05` clients
  legitimately send `_meta.progressToken` and OTel `traceparent`. Only
  the reserved `io.modelcontextprotocol/protocolVersion` key signals
  modern.
- **The `MCP-Protocol-Version` header is not a modern marker.** It
  predates `2026-07-28`, so clients that negotiated *down* to legacy
  send it on every subsequent request. Treating it as a modern signal
  serves their handshake and then bricks the session.

A port that gets either wrong still passes a naive smoke test and fails
against real clients. Both cases are pinned by fixtures.

A malformed modern-looking request is answered with modern spec errors —
never silently demoted to legacy. That is what dual-era clients rely on
to avoid falling back incorrectly.

## The parity contract

Wire behaviour is pinned by
[`sdk/tests/cross-lang/fixtures/mcp-wire.json`](../../../sdk/tests/cross-lang/fixtures/mcp-wire.json),
generated from the Go surface and compared as **raw bytes** — no JSON
decode/re-encode before comparing. Go emits objects with
lexicographically sorted keys and a trailing newline; a runtime whose
serializer differs must reorder to match, not normalize the comparison.

Run the gate:

```bash
make test-parity-mcp
```

Where this page and the fixtures disagree, **the fixtures win** and this
page is wrong.

### The fixture has two sections, and both matter

This is the non-obvious part, and it was learned the hard way.

- **`cases`** (18 of them) each get a **fresh mount**. No case can
  observe state left by another.
- **`sequences`** are the deliberate exception: ordered steps replayed
  against **one long-lived mount**, which is how adopters actually
  deploy.

Two real defect classes pass every single case and are caught only by
the sequence:

1. **A port that caches its leaf set.** `tools/list` must re-read the
   bridge's leaves every time. Go's `Leaf` wraps a live cobra command
   and re-walks its flags per request.
2. **A port that attaches lazy flags non-idempotently.** Cobra attaches
   `--help` to a command on its *first execution*, so two byte-identical
   `tools/list` requests on one mount legitimately differ across an
   intervening `tools/call` — and must differ *consistently*.

The shipped sequence, `legacy/lazy-help-flag-on-long-lived-mount`,
replays exactly that: list, invoke, list, invoke, list. A cache passes
every case and fails step 3. Non-idempotent attachment passes every case
and step 3, then fails step 5.

Run both sections. A runner that only replays `cases` is not testing the
contract.

## What is not implemented

Deprecated upstream features — Roots, Sampling, Logging, HTTP+SSE —
are unimplemented in every port, matching Go. The `tasks/*` extension
methods answer `-32601`, and per spec the surface advertises no
`extensions` map in `server/discover`, which *is* the conformant way to
not support it.

Ports do not implement the `2026-07-28` auth hardening stack (RFC 9207
issuer validation, CIMD, `application_type` registration). No SDK-side
OAuth surface exists in TypeScript, Python, Rust, or PHP today, and
inventing one per language guarantees four incompatible designs. Auth
server integration stays adopter-provided.

What *is* ported, because it is wire-visible and fixture-pinned: the
`Authorization` header check on `kit/auth-required` leaves, and the
`X-Confirm-Token` gate on `kit/requires-confirmation` leaves. The MRTR
confirmation HMAC rides along in every port except TypeScript (see
[Where the ports genuinely differ](#where-the-ports-genuinely-differ)).

## Related pages

- [expose-cli-over-mcp.md](expose-cli-over-mcp.md) — the Go reference
  surface, in depth: MRTR confirmation, origin validation, cache hints
- [serve-mcp-with-the-sdk.md](serve-mcp-with-the-sdk.md) — Go's
  SDK-backed alternative mount (prompts, resources, subscriptions)
- [cli-parity-guide.md](cli-parity-guide.md) — the wider cross-language
  behaviour contract
- [PHP SDK README](../../../sdk/experimental/php/README.md) — PSR-15
  hosting walkthrough
- [parity contracts](../../../contracts/parity/README.md) — every
  cross-language contract, including this one
- MCP specification:
  <https://modelcontextprotocol.io/specification/2026-07-28>
