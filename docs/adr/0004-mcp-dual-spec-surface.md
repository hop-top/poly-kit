# ADR 0004: Dual-spec MCP surface (2024-11-05 + 2026-07-28)

- **Status**: Accepted
- **Date**: 2026-08-13
- **Refs**:
  <https://modelcontextprotocol.io/specification/2026-07-28>,
  <https://modelcontextprotocol.io/specification/2026-07-28/basic/versioning>,
  <https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http>,
  <https://modelcontextprotocol.io/specification/2026-07-28/basic/patterns/mrtr>,
  <https://modelcontextprotocol.io/specification/2026-07-28/server/utilities/caching>,
  <https://blog.modelcontextprotocol.io/posts/2026-07-28/>

## Context

`go/transport/cmdsurface` mounts an MCP surface (`MountMCP`) that pins
`protocolVersion "2024-11-05"`: plain HTTP JSON-RPC on one POST path
(default `/mcp`) serving `initialize`, `tools/list`, and `tools/call`,
one MCP tool per leaf cobra command, JSON-RPC error codes
`-32700/-32600/-32601/-32602/-32603`, no sessions, no SSE. Destructive
leaves are blocked on remote surfaces by the policy gate in
`safety.go`; `AuthRequired` and `RequiresConfirmation` leaves are
gated on the `Authorization` and `X-Confirm-Token` headers in
`surface_mcp_call.go`.

MCP revision 2026-07-28 restructures the protocol:

1. **Stateless core.** The `initialize`/`initialized` handshake and
   `Mcp-Session-Id` are removed. Every request carries protocol
   version, client identity, and capabilities in `params._meta` under
   reserved `io.modelcontextprotocol/*` keys. Servers must implement
   `server/discover`. New spec-reserved error codes: `-32020`
   `HeaderMismatch`, `-32021` `MissingRequiredClientCapability`,
   `-32022` `UnsupportedProtocolVersion`.
2. **MRTR** (multi round-trip requests). Results carry a `resultType`
   (`"complete"` or `"input_required"`); a server needing client input
   returns `resultType: "input_required"` with `inputRequests` and an
   opaque `requestState`; the client retries the original call with
   `inputResponses` (+ echoed `requestState`). Replaces
   server-initiated requests over open streams.
3. **Header routing.** Requests must include `MCP-Protocol-Version`,
   `Mcp-Method`, and (for `tools/call` / `resources/read` /
   `prompts/get`) `Mcp-Name` HTTP headers mirroring body fields, so
   gateways can route without parsing bodies. Servers must validate
   header/body agreement (`400` + `-32020` on mismatch).
4. **Cacheable list results.** `server/discover`, `tools/list`,
   `prompts/list`, `resources/list`, `resources/templates/list`, and
   `resources/read` complete-results carry `ttlMs` (integer ms, must
   be `>= 0`) and `cacheScope` (`"public"` | `"private"`).
5. **Auth hardening.** RFC 9207 issuer validation required,
   `application_type` at registration, credentials bound to the
   issuing authorization server, DCR deprecated in favor of Client ID
   Metadata Documents (CIMD).
6. **Tasks extension.** Polling tasks move to extension
   `io.modelcontextprotocol/tasks` (`tasks/get`, `tasks/update`),
   negotiated via `capabilities.extensions`.
7. **Deprecations.** Roots, Sampling, Logging, and the legacy
   HTTP+SSE transport are deprecated upstream. kit's surface
   implements none of them.

Hard invariant for this work: **2024-11-05 behavior is preserved
byte-for-byte. Additive only. No deprecation.** Both spec versions are
served from one mount with per-request version detection. This ADR
fixes the detection precedence, the handler split, the option and
config surface, the safety-gate binding, and the toolspec-adapter
impact, precisely enough that implementation requires no further
design decisions.

## Decision

Split the MCP surface into two version handlers behind the existing
single `MountMCP` mount. The legacy handler is today's `mcpHandler`,
untouched. A new modern handler implements 2026-07-28. A thin era
dispatcher routes each POST to exactly one handler using the
per-request detection rules below. Default configuration enables both
versions; existing `MountMCP` calls compile and behave unchanged.

This mirrors the spec's own dual-era server model: "a request carrying
modern per-request `_meta` is served statelessly according to this
revision; an `initialize` request selects legacy semantics", and a
dual-era server "MAY serve both eras concurrently on the same endpoint".

### Per-request era detection (normative)

**Modern markers.** For a request `R`, the marker set `M(R)` is:

| ID | Marker |
| -- | ------ |
| M1 | HTTP header `Mcp-Method` present |
| M2 | HTTP header `Mcp-Name` present |
| M3 | body `params._meta` contains the key `io.modelcontextprotocol/protocolVersion` (key presence only; the value is not inspected at detection time) |
| M4 | body `method == "server/discover"` |

Two deliberate non-markers:

- The mere presence of `params._meta` is **not** a marker: 2024-11-05
  clients legitimately send `_meta.progressToken` (and OTel
  `traceparent`/`tracestate`/`baggage`). Only the reserved
  `protocolVersion` key signals the modern era.
- Presence of the `MCP-Protocol-Version` **header** is **not** a
  marker. The header predates 2026-07-28 (introduced with the
  2025-06-18 transport), so SDK clients that negotiated down to
  2024-11-05 via kit's `initialize` handshake send it on every
  subsequent request; treating it as a modern signal would serve
  their handshake and then brick the session. On the legacy path it
  is tolerated and ignored, like `Mcp-Session-Id` and
  `Last-Event-ID`. Nothing is lost: a conforming 2026-07-28 request
  always carries M1 (`Mcp-Method` is required on every request) and
  M3, so no modern request is ever detected by that header alone.
  Once a request *is* routed modern, the header becomes mandatory
  again (V4).

**Precedence** (first rule that applies wins):

- **D1 — parse.** Read and JSON-decode the body once, in the
  dispatcher. Unreadable body → `-32603` at HTTP 400; unparseable
  JSON → `-32700` at HTTP 400 — both byte-identical to today's
  responses, regardless of any headers present.
- **D2 — `initialize` is legacy, unconditionally.** `method ==
  "initialize"` routes to the legacy handler even when modern markers
  are present. Rationale: the spec's dual-era rule says `initialize`
  selects legacy semantics; a confused client gets a working legacy
  handshake, the most recoverable outcome. Modern clients never send
  `initialize`.
- **D3 — any marker routes modern.** If `M(R)` is non-empty, route to
  the modern handler. Incomplete or contradictory modern requests are
  **not** demoted to legacy; the modern handler rejects them with
  spec-defined *modern* errors (see validation order). This is what
  dual-era clients rely on to avoid falling back incorrectly: a
  recognized modern JSON-RPC error body identifies a modern server.
- **D4 — otherwise legacy.** No markers → legacy handler. This is the
  byte-for-byte preservation path: every request a legacy client can
  send — including mid-era SDK clients that negotiated 2024-11-05 via
  the handshake and therefore send `MCP-Protocol-Version` on every
  subsequent request — has an empty marker set (or is `initialize`)
  and takes today's exact code path.

**Interaction with enabled versions:**

- Both enabled (default): D1–D4 as above.
- Legacy only: `MountMCP` installs today's `mcpHandler.serveHTTP`
  directly; the dispatcher is not in the path and markers are ignored
  exactly as today.
- Modern only: every request routes to the modern handler and is
  handled per the normal V1–V9 order — no special-casing of
  `initialize` anywhere. A bare legacy `initialize` therefore fails
  V3 (`-32602` @ 400, missing required `_meta`). Whenever the modern
  handler rejects a request whose `method` is `"initialize"`, the
  error `message` additionally names the supported versions (spec
  SHOULD: a modern-only server names its supported versions "in any
  error it returns" to `initialize`, since legacy clients have no
  fall-forward mechanism; the spec's compatibility matrix likewise
  expects this rejection to be a 400 via normal server validation).

**Worked edge cases** (both versions enabled):

| Request | Route | Response |
| ------- | ----- | -------- |
| bare `initialize` | legacy | today's initialize result (`2024-11-05`) |
| `initialize` + any marker | legacy | same |
| bare `tools/list` / `tools/call` | legacy | today's responses |
| `tools/list` / `tools/call` with only an `MCP-Protocol-Version: 2024-11-05` header (mid-era legacy-negotiated SDK client) | legacy | today's responses; header ignored |
| bare unknown method | legacy | `-32601` at HTTP **200** (today's behavior, preserved) |
| `tools/call` with `_meta` protocolVersion key only (M3), no headers | modern | `-32602` at 400 (V3: `clientCapabilities` missing) |
| `tools/call` with complete `_meta`, no headers | modern | `-32020` at 400 (V4: missing `MCP-Protocol-Version` header) |
| `tools/call` with M1 only (no `_meta`) | modern | `-32602` at 400 (missing required `_meta` fields) |
| bare `server/discover` | modern | `-32602` at 400 (missing required `_meta` fields; the spec-mandated modern response, though `-32602` alone is not one of the distinctively modern `-3202x` codes) |
| unknown method + valid modern envelope | modern | `-32601` at HTTP **404** |
| notification (no `id`) + markers | modern | HTTP 202, empty body, not processed |
| `id: null` + markers | modern | `-32600` at 400 |
| unparseable body (any headers) | dispatcher | `-32700` at 400, identical to today |

### Modern handler validation order (normative)

Checks run in this order; the first failure responds and stops. HTTP
status is 400/404 only where the spec mandates it; application-level
JSON-RPC errors ride HTTP 200 (matching legacy's convention and valid
under 2026-07-28, which fixes statuses only for the cases below).

| # | Check | Failure |
| - | ----- | ------- |
| V1 | `jsonrpc` member absent or `"2.0"` (same tolerance as legacy) | `-32600` @ 400 |
| V2 | `id` present → request; absent → notification (respond HTTP 202, empty body, discard); present id MUST be a JSON string or a JSON number with no fractional part (base JSON-RPC also allows `null`, but this revision forbids it) — `null`, boolean, float, object, and array ids are all malformed | `-32600` @ 400 |
| V3 | `params._meta` carries required keys `io.modelcontextprotocol/protocolVersion` and `io.modelcontextprotocol/clientCapabilities` (`clientInfo` optional) | `-32602` @ 400 |
| V4 | `MCP-Protocol-Version` header present and equal to the `_meta` protocolVersion value | `-32020` @ 400 |
| V5 | requested version supported; the modern handler supports exactly `"2026-07-28"` | `-32022` @ 400, `data: {"supported": ["2026-07-28"], "requested": <value>}` |
| V6 | `Mcp-Method` header present and equal to body `method` | `-32020` @ 400 |
| V7 | `tools/call` only: `Mcp-Name` header present and, after Base64-sentinel decoding (`=?base64?...?=`), equal to `params.name`. On other methods the header is ignored | `-32020` @ 400 |
| V8 | method is one of `server/discover`, `tools/list`, `tools/call` | `-32601` @ 404 |
| V9 | per-method params (e.g. missing/unknown tool name) | `-32602` @ 200 |

`-32022`'s `supported` list deliberately excludes `"2024-11-05"`:
`supported` names versions a client may select *per-request*; the
legacy revision is only reachable through its handshake, which rules
D2/D4 serve. Inbound `Mcp-Param-*` headers are ignored (kit defines no
`x-mcp-header` annotations, so none are "recognized"; unrecognized
param headers must be forwarded/ignored per spec). `Mcp-Session-Id`
and `Last-Event-ID`, if sent by mid-era clients, are ignored — never
minted, never echoed.

### Methods and wire shapes (modern)

Every modern result envelope carries `resultType` (always
`"complete"` until MRTR confirmation lands) and a result-level `_meta`
with `io.modelcontextprotocol/serverInfo` built from the mount's
configured server name/version (spec SHOULD; same values the legacy
`initialize` reports).

- **`server/discover`** →
  `{resultType, supportedVersions: ["2026-07-28"], capabilities:
  {"tools": {}}, _meta, ttlMs, cacheScope}`. No `listChanged` flag
  (notifications not implemented), no `extensions` map (none
  supported), no `instructions`.
- **`tools/list`** → `{resultType, tools: [...], ttlMs, cacheScope,
  _meta}`. Tool envelopes are built by the same `buildToolEnvelope`
  used today (`name` = dotted leaf path, `description`,
  `inputSchema` from pflag reflection). Optional 2026-07-28
  descriptor fields (`title`, `icons`, `outputSchema`, `annotations`,
  `x-mcp-header`) are not emitted. Pagination is not implemented; a
  `cursor` param is ignored and no `nextCursor` is returned.
- **`tools/call`** → `{resultType, content: [...], isError,
  structuredContent?, _meta}`. The `content` block layout reuses
  today's `renderCallResult` exactly (stdout text block, `[stderr] `
  block when present, JSON text block when `Result.Data` is present);
  additionally `structuredContent` is set to `Result.Data` when
  non-nil (the JSON text block doubles as the spec-recommended
  serialized fallback). `isError: true` iff `ExitCode != 0`, as
  today. `tools/call` results carry **no** cache hints (not a
  cacheable operation).

**Cache hints.** `ttlMs`/`cacheScope` appear on `server/discover` and
`tools/list` complete-results only. Defaults: `ttlMs: 0` (immediately
stale — honest, since `Expose`/`Hide` can mutate the leaf set at
runtime and no `list_changed` notification exists) and
`cacheScope: "private"` (safe default; adopters whose tool list is
caller-independent opt into `public`). Both are adopter-tunable via
option and config block below.

**HTTP verbs.** When the modern version is enabled, `MountMCP`
additionally registers GET and DELETE handlers at the mount path
returning HTTP 405 (spec SHOULD for post-session servers). The POST
route is byte-for-byte unaffected. Responses are always single JSON
objects (`Content-Type: application/json`); the SSE response-stream
option is not used — clients must accept both per spec.

**Origin.** `WithMCPOriginAllowlist` (below) enables Origin-header
validation on the modern path: when configured and a request carries
an `Origin` not in the allowlist → HTTP 403. Unset = no check
(deployment-proxy responsibility; see Acknowledged quirks).

### Package and file layout

All new code lives in `go/transport/cmdsurface` (same package,
following the existing one-file-per-concern surface convention).
Existing files `surface_mcp.go`, `surface_mcp_list.go`,
`surface_mcp_call.go` keep their current handler code unchanged; the
only permitted edit is `MountMCP`'s body (dispatcher construction and
new-option plumbing) plus new `MCPOption` declarations alongside the
existing ones.

| File | Contents |
| ---- | -------- |
| `surface_mcp_dispatch.go` | era detection + routing: `mcpEra` (unexported int enum: `mcpEraLegacy`, `mcpEraModern`), `detectMCPEra`, `mcpDispatcher` (holds the parsed body + both handlers), 405 handlers for GET/DELETE |
| `surface_mcp_modern.go` | `mcpModernHandler` struct; V1–V8 validation; `server/discover`; modern error writers (`-32020/-32021/-32022` + status mapping); result-`_meta` serverInfo injection; cache-hint application; Base64-sentinel decoding helper |
| `surface_mcp_modern_list.go` | modern `tools/list` |
| `surface_mcp_modern_call.go` | modern `tools/call`: V7/V9, pre-flight gates, confirmation-gate slot, invoke, render |

Pinned identifiers (so later work and tests agree on names):

```go
// Exported (new):
type MCPSpecVersion string
const (
    MCPSpec20241105 MCPSpecVersion = "2024-11-05"
    MCPSpec20260728 MCPSpecVersion = "2026-07-28"
)
type MCPCacheScope string
const (
    MCPCacheScopePublic  MCPCacheScope = "public"
    MCPCacheScopePrivate MCPCacheScope = "private"
)
func WithMCPSpecVersions(versions ...MCPSpecVersion) MCPOption
func WithMCPCacheHints(ttl time.Duration, scope MCPCacheScope) MCPOption
func WithMCPOriginAllowlist(origins ...string) MCPOption

// Unexported anchors:
// headerMCPProtocolVersion = "MCP-Protocol-Version"
// headerMCPMethod          = "Mcp-Method"
// headerMCPName            = "Mcp-Name"
// metaKeyProtocolVersion   = "io.modelcontextprotocol/protocolVersion"
// metaKeyClientInfo        = "io.modelcontextprotocol/clientInfo"
// metaKeyClientCapabilities= "io.modelcontextprotocol/clientCapabilities"
// metaKeyServerInfo        = "io.modelcontextprotocol/serverInfo"
// mcpErrHeaderMismatch          = -32020
// mcpErrMissingClientCapability = -32021
// mcpErrUnsupportedVersion      = -32022
// mcpModernProtocolVersion      = "2026-07-28"
```

The existing `mcpProtocolVersion` const ("2024-11-05") and every
legacy symbol keep their names and values.

### MountMCP option surface

`MCPOption` stays the option type; existing calls (`MountMCP(b, r)`,
with or without `WithMCPPath` / `WithMCPServerInfo`) compile unchanged
and, because both versions default to enabled, behave identically for
all traffic a 2024-11-05 client can produce.

| Option | Semantics |
| ------ | --------- |
| `WithMCPSpecVersions(versions...)` | Replaces the enabled-version set. Absent → both enabled. Empty call or an unrecognized version → `MountMCP` returns an error (mount-time refusal, matching the surface's all-or-nothing mount convention). Duplicates are deduplicated. |
| `WithMCPCacheHints(ttl, scope)` | Sets `ttlMs` (from `ttl`, truncated to whole ms; negative → error at mount) and `cacheScope` for `server/discover` + `tools/list`. Absent → `0` / `"private"`. Unknown scope → mount error. |
| `WithMCPOriginAllowlist(origins...)` | Enables Origin validation on the modern path against the given exact-match origins. Absent → no Origin check. |
| `WithMCPPath(path)` (existing) | Unchanged; the dispatcher mounts at this path. |
| `WithMCPServerInfo(name, version)` (existing) | Unchanged; now also feeds `server/discover` and modern result `_meta` serverInfo. |

### Config block

Following the `config_blocks.go` pattern (typed YAML shape, parsed by
`Load`, translated by adopters — `FromConfig` does **not** mount
surfaces, consistent with the webhook/bus/cron blocks):

```go
// config_blocks.go
type MCPConfig struct {
    SpecVersions    []string `yaml:"spec_versions,omitempty"` // empty = both
    Path            string   `yaml:"path,omitempty"`          // empty = "/mcp"
    CacheTTLMs      int      `yaml:"cache_ttl_ms,omitempty"`  // 0 = stale
    CacheScope      string   `yaml:"cache_scope,omitempty"`   // "" = private
    OriginAllowlist []string `yaml:"origin_allowlist,omitempty"`
}

// config.go — Config gains:
//   MCP *MCPConfig `yaml:"mcp,omitempty" json:"mcp,omitempty"`
```

Pointer type so absence is distinguishable from an explicit empty
block (same rationale as `Telemetry`). Adopters read `cfg.MCP` and
translate to the options above when calling `MountMCP`.

### Safety-gate binding

Both handlers dispatch through the same `Bridge.Invoke`, so the
`safety.go` policy gate applies identically and automatically:
`Policy.Allowed` keeps destructive leaves unreachable on `SurfaceMCP`
unless `AllowDestructiveOn` names it, and per-leaf `Enabled`
filtering applies in `tools/list` and `tools/call` on both paths.

**One surface, not two.** The modern handler reuses
`Surface("mcp")`. No new `Surface` value is introduced: enablement,
policy, YAML `surfaces:` blocks, and sink filters treat both spec
versions as one transport, which is exactly the dual-stack promise
(one mount, one exposure decision). The spec version is recorded for
audit sinks in `Meta.Extra`: the modern handler sets
`Extra["mcp_spec_version"] = "2026-07-28"` and, when `clientInfo` is
present, `Extra["mcp_client_name"]` / `Extra["mcp_client_version"]`.
The legacy handler sets nothing new (unchanged code path).

**Pre-flight gates (modern), mirroring legacy exactly:**

| Condition | Modern response |
| --------- | --------------- |
| unknown / not-enabled leaf | `-32602` @ 200, "unknown tool" |
| `Class.AuthRequired` and no `Authorization` header | `isError` result envelope @ 401 |
| `Class.RequiresConfirmation` and no `X-Confirm-Token` header (default gate) | `isError` result envelope @ 428 |
| `ErrDestructiveBlocked` from `Bridge.Invoke` | `isError` result envelope @ 200 |
| other invoke errors / non-zero `ExitCode` | `isError` result envelope @ 200 |

All `isError` envelopes on the modern path carry
`resultType: "complete"` (tool-execution errors are complete results
per spec).

**MRTR confirmation slot.** The modern `tools/call` path is
structured as: resolve leaf → auth gate → *confirmation gate* →
`Bridge.Invoke` → render. The confirmation gate is an unexported
strategy slot on `mcpModernHandler` whose default implementation is
the `X-Confirm-Token` check above. A later change replaces the
default with an MRTR flow, pinned here so it needs no new design:

- Applies only when the request's
  `io.modelcontextprotocol/clientCapabilities` declares `elicitation`;
  otherwise the header gate remains (never `-32021` for confirmation —
  the capability is optional because a fallback exists).
- First call on a `RequiresConfirmation` leaf without a valid
  confirmation returns `resultType: "input_required"` with
  `inputRequests` under the single reserved key `"confirm"` (an
  `elicitation/create` form request asking the user to approve the
  invocation) and a `requestState`.
- `requestState` is integrity-protected (HMAC-SHA-256; key is
  process-local random per mount by default) over: leaf path key, a
  SHA-256 digest of the canonically-serialized arguments, the caller
  principal when known, and a short expiry. On retry the gate verifies
  the state, requires `inputResponses.confirm.action == "accept"`, and
  proceeds; decline/cancel → `isError` "confirmation declined".
  Two failure cases are distinct: an **expired** (but authentic)
  state is a routine re-prompt → a fresh `input_required` result
  (spec: re-request missing information rather than error); a state
  that **fails HMAC verification** is rejected (spec MUST) — it is
  never honored, the event is logged as a security-relevant audit
  event, and only then does the gate issue a fresh `input_required`
  with newly minted state. Tampering thus causes nothing worse than
  request failure, but is never silently treated as a re-prompt.
- Interim `input_required` results carry no cache hints and are never
  cached.
- MRTR never relaxes the destructive ceiling: `ErrDestructiveBlocked`
  is unaffected by any confirmation outcome. Confirmation and
  destructive lockdown remain independent gates, as today.

### Toolspec adapter (`go/ai/toolspec/adapters/mcp.go`)

**Decision: no change.** The adapter renders a *static tool
descriptor* (`{name, description, inputSchema}`) for
`<tool> spec --format mcp`; 2026-07-28 leaves every required
descriptor field unchanged and only adds optional ones (`title`,
`icons`, `outputSchema`, `annotations`, `x-mcp-header`). The
descriptor therefore remains valid under both revisions. The new wire
fields (`resultType`, `ttlMs`, `cacheScope`, result `_meta`) belong to
response envelopes, which the adapter never emits. The deliberate
duplication between the adapter and the live surface (noted at the
top of `surface_mcp.go`) stands: the adapter's action-enum
aggregation model is orthogonal to the surface's one-tool-per-leaf
model, and coupling them for this change would buy nothing.

## Gap matrix

| 2026-07-28 change | Current state (verified in code) | Planned mechanism |
| ----------------- | -------------------------------- | ----------------- |
| Stateless core: per-request `_meta` (protocolVersion / clientInfo / clientCapabilities), `initialize` + `Mcp-Session-Id` removed, `server/discover` mandatory, `-32020/-32021/-32022` | `surface_mcp.go` pins `2024-11-05`, serves `initialize`; never reads `_meta`; sessions never implemented (nothing to remove) | Era dispatcher (D1–D4) + `mcpModernHandler` with V1–V9 validation; `server/discover`; serverInfo in result `_meta`; new error codes |
| MRTR: `resultType`, `InputRequiredResult` (`inputRequests`/`requestState`), retry with `inputResponses` | Absent; strictly single round-trip; confirmation via `X-Confirm-Token` header | `resultType: "complete"` on every modern result now; confirmation-gate slot in modern `tools/call`; full `input_required` confirmation flow later per the pinned MRTR design |
| Header routing: `MCP-Protocol-Version`, `Mcp-Method`, `Mcp-Name` required; header/body validation; Base64 sentinel; `Mcp-Param-*` | All headers ignored | Modern handler requires + validates (V4, V6, V7) with `-32020` @ 400; sentinel decoding for `Mcp-Name`; `x-mcp-header` not emitted, so inbound `Mcp-Param-*` are unrecognized and ignored |
| Cacheable list results: `ttlMs` + `cacheScope` on discover/list/read ops | Absent | Emitted on `server/discover` + `tools/list` complete-results; defaults `0` / `"private"`; `WithMCPCacheHints` + `mcp:` config block |
| Auth hardening: RFC 9207 issuer validation, `application_type`, credential binding, CIMD replaces DCR | MCP surface checks bearer *presence* only (`Class.AuthRequired`); no OAuth AS/RS in the bridge. `surface_oauth.go` is the OAuth *client-side* callback surface — the one component RFC 9207's client obligation binds | Split by where each mechanism lives. Kit-side (shipped): RFC 9207 `iss` validation on the OAuth callback via `OAuthProvider.ExpectedIssuer` — `iss` required + exact string match, rejected before provider-error and state handling; validated issuer forwarded through `Meta.Extra["oauth_issuer"]` and a `FlagFromQuery` `"iss"` mapping. Deployment-side: `application_type` at registration, issuer-bound token rejection, CIMD — these bind the AS/RS in front of the mount; adopter guidance in `ADOPTER_GUIDE.md`. The MCP mount stays auth-scheme-agnostic; no MCP wire change |
| Tasks extension `io.modelcontextprotocol/tasks` (`tasks/get`, `tasks/update`) | Absent | Not implemented. `capabilities.extensions` omitted from `server/discover` (= unsupported); `tasks/*` → `-32601` @ 404. The extensions map is the designated slot if this is ever added |
| Deprecations: Roots, Sampling, Logging, HTTP+SSE transport | None implemented | Nothing to remove or add. GET/DELETE → 405 when modern enabled; `Mcp-Session-Id` / `Last-Event-ID` ignored |
| Tool-descriptor shape (`toolspec/adapters/mcp.go` + live `tools/list` envelopes) | `{name, description, inputSchema}` in both places | Unchanged in both places; new optional descriptor fields not emitted; adapter decision recorded above as "no change" |

## Non-goals

Deliberately excluded from this work (each would be its own decision):

- `prompts/*`, `resources/*`, `subscriptions/listen`, list-changed
  notifications, and SSE response streams.
- Pagination of `tools/list` (`cursor` ignored, no `nextCursor`).
- Emitting `title`, `icons`, `outputSchema`, `annotations`, or
  `x-mcp-header` in tool descriptors.
- The tasks extension and any `capabilities.extensions` entry.
- OAuth authorization-server / resource-server mechanics inside the
  bridge: client registration (DCR or CIMD), token issuance, and
  issuer-bound token rejection. The MCP mount stays
  auth-scheme-agnostic. (The RFC 9207 *client-side* `iss` check ships
  on the OAuth callback surface — client-half validation, not an
  AS/RS feature.)
- A per-version `Surface` value or per-version enablement.

## Consequences

### Positive

- 2024-11-05 clients are untouched by construction: their traffic —
  including the `MCP-Protocol-Version` header that mid-era SDK
  clients send after negotiating down via the handshake — has an
  empty marker set and reaches the exact code that serves it today,
  which the legacy conformance cassettes lock.
- Modern clients (and dual-era clients probing per the spec's
  backward-compatibility algorithm) get spec-correct behavior: every
  probe outcome in the spec's compatibility matrix maps to a defined
  row in the detection table.
- Gateways can route/authorize on `Mcp-Method`/`Mcp-Name` with
  header/body agreement enforced server-side.
- The safety gate is provably shared: one `Bridge.Invoke` path, one
  `Surface`, one policy — no way to reach a leaf on the modern path
  that the legacy path would have blocked.

### Negative

- Two handlers to maintain until 2024-11-05 support is someday
  retired (explicitly not scheduled; "no deprecation" is the
  invariant).
- The modern handler's strictness (V3–V7) means hand-rolled curl
  requests need three headers and two `_meta` keys to exercise it —
  a documentation burden the adopter guide must absorb.
- `ttlMs: 0` defaults forfeit caching benefits until adopters opt in.

### Neutral

- `resolveLeaf`, `collectFlags`, `buildToolEnvelope`,
  `renderCallResult`, and the flag-type mapping are shared by both
  handlers, so schema drift between eras cannot happen.
- The config block is declarative-only, like webhook/bus/cron;
  `FromConfig` still mounts nothing.

## Acknowledged quirks

- **Legacy `-32601` at HTTP 200 stands.** Today's legacy handler
  returns method-not-found with HTTP 200; the modern handler must use
  404. The asymmetry is deliberate — changing the legacy status would
  violate byte-for-byte preservation.
- **`initialize` + modern markers routes legacy.** The spec leaves
  this combination undefined (its two dual-era bullets conflict);
  D2 pins method-wins because a working handshake is the most
  recoverable answer for a confused client.
- **`supported` excludes `"2024-11-05"`.** A `-32022` retry loop can
  only carry per-request versions; advertising the handshake-only
  legacy version there would send modern clients into a dead end.
- **Origin validation is opt-in.** The spec says servers MUST
  validate `Origin`; kit cannot judge validity without adopter input,
  so the check activates only with `WithMCPOriginAllowlist`.
  Adopters binding to localhost should configure it (DNS-rebinding
  defense); those behind an authenticating proxy may delegate to it.
- **`Mcp-Name` comparison decodes the Base64 sentinel first.** Dotted
  kit tool names are header-safe ASCII, but conforming clients may
  still send `=?base64?...?=`; V7 decodes before comparing, and a
  plain value that merely *looks* like the sentinel is treated as
  encoded per spec.
- **Flag values in `Meta.Extra` are strings.** `mcp_spec_version` and
  the client-info keys ride the existing `map[string]string` bag; no
  `Meta` struct change.

## Alternatives considered

- **Second mount path (e.g. `/mcp2026`).** Rejected: violates the
  one-mount requirement, forces every gateway and client config to
  know kit-specific topology, and the spec explicitly blesses serving
  both eras on one endpoint.
- **New `SurfaceMCP2026` value.** Rejected: doubles every enablement
  decision (YAML, `Expose`/`Hide`, policy, sinks) for what is one
  transport; risks a leaf accidentally exposed on one era only —
  exactly the drift the shared-gate invariant forbids.
- **Body-only detection (ignore headers as signals).** Rejected: a
  modern client with a broken `_meta` but correct headers would fall
  through to the legacy handler and receive non-spec errors,
  defeating the spec's fallback algorithm, which keys on recognized
  modern error bodies.
- **`MCP-Protocol-Version` header as a (value-sensitive) marker.**
  Rejected: header *presence* would route mid-era legacy-negotiated
  SDK clients — which send it on every post-`initialize` request —
  to the modern handler and brick their sessions; a value-sensitive
  variant (marker only when the value is a modern-servable version)
  avoids that but adds a second value-inspection rule for zero
  coverage, since every conforming modern request already carries
  M1 and M3.
- **Strict rejection of `initialize` + modern markers.** Rejected:
  adds a failure mode the spec doesn't define, for no interop gain.
- **Teaching the toolspec adapter the 2026 descriptor fields now.**
  Rejected: all-new fields are optional, kit has no data for them
  (`outputSchema`, `icons`), and descriptor emission is orthogonal to
  the transport work. Recorded as "no change" rather than silence so
  the question stays answered.

## References

- MCP 2026-07-28 specification: overview / `_meta`
  (<https://modelcontextprotocol.io/specification/2026-07-28/basic/index>),
  versioning & dual-era compatibility
  (<https://modelcontextprotocol.io/specification/2026-07-28/basic/versioning>),
  Streamable HTTP transport & header validation
  (<https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http>),
  MRTR (<https://modelcontextprotocol.io/specification/2026-07-28/basic/patterns/mrtr>),
  tools (<https://modelcontextprotocol.io/specification/2026-07-28/server/tools>),
  discovery (<https://modelcontextprotocol.io/specification/2026-07-28/server/discover>),
  caching (<https://modelcontextprotocol.io/specification/2026-07-28/server/utilities/caching>).
- Release announcement:
  <https://blog.modelcontextprotocol.io/posts/2026-07-28/>.
- RFC 9207 (OAuth 2.0 Authorization Server Issuer Identification):
  <https://www.rfc-editor.org/rfc/rfc9207>.
