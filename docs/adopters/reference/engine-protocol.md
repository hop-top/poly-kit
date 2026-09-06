# Engine Protocol Reference

This protocol is what makes kit apps language-agnostic peers.
A Go app using kit natively and a TS app using kit serve speak
the SAME sync/peer/bus wire protocol. This spec IS the interop
contract.

The protocol-of-record decisions and per-row migration table for
the 2026-05 reconciliation pass live in
[ADR-0018](../../contributors/adr/0018-engine-sdk-protocol-reconciliation.md), backed
by [`docs/contributors/audits/engine-sdk-drift.md`](../../contributors/audits/engine-sdk-drift.md).
Implementations:
[`cmd/kit/serve.go`](../../../cmd/kit/serve.go),
[`engine/sdk/ts-kit-engine`](../../../engine/sdk/ts-kit-engine/README.md),
[`engine/sdk/py-kit-engine`](../../../engine/sdk/py-kit-engine/README.md).

## Conventions

- Base URL: `http://localhost:<port>` (port from engine stdout)
- Paths below are written `/:type/:id` for readability. The router
  itself uses Go 1.22 wildcard syntax (`/{type}/{id}`); the wire paths
  are identical either way.
- The **trailing slash is significant** on collection routes: the
  registered patterns are `POST /:type/` and `GET /:type/`, so
  `GET /notes` does not route — use `GET /notes/`.
- Content-Type: `application/json` for all request/response bodies
- `kit serve` prints startup JSON to stdout:
  `{"port": 9090, "pid": 12345, "token": "...", "shutdown_token": "..."}`
  — four keys, one line, first line of stdout. `shutdown_token` is
  distinct from `token`.
- Auth is decided by **method, not path**: every `GET` and `HEAD` is
  public, whatever the route. Every other method requires
  `Authorization: Bearer <token>`, accepting either `token` or
  `shutdown_token`. There are no path exemptions, so
  `/identity`, `/peers`, `/sync/status`, `/sync/pull` and `/events` are
  all readable without a token.
- `POST /shutdown` is stricter than the middleware: it accepts **only**
  `shutdown_token` and answers 401 to the ordinary auth token.
- Error format (all non-2xx responses) carries **four** keys. `message`
  and `error` always hold the same string; `error` is a legacy duplicate
  retained for existing SDKs:

```json
{"status": 404, "code": "not_found", "message": "not found", "error": "not found"}
```

`code` values: `invalid_json`, `invalid_type`, `bad_request`,
`unauthorized`, `not_found`, `conflict`, `internal_error`, and `error`
as the fallback. The auth middleware emits the bare three-key form
without `error`.

| Status | Meaning             |
|--------|---------------------|
| 200    | OK                  |
| 201    | Created             |
| 204    | No Content (delete) |
| 400    | Bad Request         |
| 404    | Not Found           |
| 409    | Conflict            |
| 412    | Precondition Failed |
| 500    | Internal Error      |

---

## Documents

### Create Document

```
POST /:type/
```

**Request:**

| Header       | Value              |
|--------------|--------------------|
| Content-Type | application/json   |

```json
{
  "id": "string (optional, auto-generated if omitted)"
}
```

The request body is the document data itself. If the top-level JSON
object contains an `id` string, that value becomes the document ID;
otherwise the engine generates one.

**Response (201):**

```json
{
  "type": "string",
  "id": "string",
  "data": {},
  "created_at": "RFC3339",
  "updated_at": "RFC3339"
}
```

**curl:**

```sh
curl -X POST http://localhost:9090/notes/ \
  -H 'Content-Type: application/json' \
  -d '{"title":"Hello","body":"world"}'
```

---

### List Documents

```
GET /:type/?limit=N&offset=N&sort=field&search=term
```

**Query params (all optional):**

| Param  | Type   | Default | Notes               |
|--------|--------|---------|---------------------|
| limit  | int    | 100     | max items; applied in the store, not at the HTTP layer. Non-integer or negative → 400 `invalid limit` |
| offset | int    | 0       | pagination offset. Non-integer or negative → 400 `invalid offset` |
| sort   | string | `created_at` | only `id`, `created_at`, `updated_at`. Any other value **silently falls back** to `created_at` — no 400 |
| search | string | —       | SQL `LIKE '%term%'` substring match against the serialized `data` column; not field-scoped, not full-text |

Ordering is always ascending. There is no descending option: no `-`
prefix, no `:desc` suffix, no separate `order` parameter.

An empty result is `[]`, never `null`.

**Response (200):**

```json
[
  {"type":"notes","id":"abc","data":{},"created_at":"...","updated_at":"..."}
]
```

**curl:**

```sh
curl http://localhost:9090/notes/?limit=10&offset=0
```

---

### Get Document

```
GET /:type/:id
```

**Response (200):**

```json
{
  "type": "notes",
  "id": "abc",
  "data": {"title": "Hello"},
  "created_at": "2026-04-19T10:00:00Z",
  "updated_at": "2026-04-19T10:05:00Z"
}
```

**404** if not found.

**curl:**

```sh
curl http://localhost:9090/notes/abc
```

---

### Update Document

```
PUT /:type/:id
```

**Request:**

```json
{
  "title": "Updated"
}
```

**Response (200):** full document with new `updated_at`.

**404** if the document does not exist. `PUT` returns 200, 400
(`invalid type` / `invalid json`, or a malformed `If-Match`), 404, or
412 when an `If-Match` guard does not match.

**Optimistic concurrency (optional).**

`GET /:type/:id` returns an `ETag` holding the document's current
version id. Send it back as `If-Match` to make a write conditional:

- header absent: unconditional write, last writer wins
- header matches the current version: write applies, response carries
  the new `ETag`
- header names any other version: **412**, and nothing is written

Only a single strong entity tag is accepted. `*`, weak tags (`W/"..."`)
and tag lists are refused with **400** rather than silently treated as
unconditional, so a client never believes it holds a guard it does not.

**curl (conditional):**

```sh
etag=$(curl -sI http://localhost:9090/notes/abc | grep -i '^etag:' | cut -d' ' -f2 | tr -d '\r')
curl -X PUT http://localhost:9090/notes/abc \
  -H 'Content-Type: application/json' \
  -H "If-Match: $etag" \
  -d '{"title":"Updated"}'
```

**curl:**

```sh
curl -X PUT http://localhost:9090/notes/abc \
  -H 'Content-Type: application/json' \
  -d '{"title":"Updated"}'
```

---

### Delete Document

```
DELETE /:type/:id
```

**Response:** 204 No Content.

**404** if not found.

**curl:**

```sh
curl -X DELETE http://localhost:9090/notes/abc
```

---

### Document History

```
GET /:type/:id/history
```

Returns version list (newest first).

**Response (200):**

```json
{
  "versions": [
    {
      "version": 3,
      "data": {},
      "timestamp": "RFC3339",
      "operation": "update"
    }
  ]
}
```

`version` is the 1-based sequence number. `operation` is derived from
it, not stored: `create` when `version == 1`, `update` for every other
entry. A deleted document's history reports no `delete` operation.

This shape is pinned by the cross-language SDK parity test, so the keys
`version`, `data`, `timestamp` and `operation` are load-bearing for the
TS and Python ports.

**curl:**

```sh
curl http://localhost:9090/notes/abc/history
```

---

### Revert Document

```
POST /:type/:id/revert
```

**Request:**

```json
{"version": 2}
```

**Response (200):** the full document envelope at the reverted state,
not a version entry.

Revert **appends** a new version rather than truncating history, so the
version list grows by one.

**409** if version does not exist.

**curl:**

```sh
curl -X POST http://localhost:9090/notes/abc/revert \
  -H 'Content-Type: application/json' \
  -d '{"version":2}'
```

---

## Branching

Three additive routes plus a query parameter on history. Surfaces
the branching public API on `VersionedDocumentStore`. Schema is
unchanged; existing linear callers see no behavioral difference.
SDK parity (TS / Python) is gated on a protocol reconcile — SDKs do
not yet expose these routes.

Branch identity is the head version_id; there is no separate branch
entity. A linear history has exactly one head; a branched history
has two or more.

### List Branches

```
GET /:type/:id/branches
```

Returns the heads (tips) of the version DAG for `(type, id)`,
ordered most-recent-first.

**Response (200):**

```json
{
  "heads": [
    {
      "version_id": "v_abc123",
      "seq": 4,
      "parent_ids": ["v_abc100"],
      "timestamp": "2026-05-07T10:05:00Z"
    },
    {
      "version_id": "v_def456",
      "seq": 3,
      "parent_ids": ["v_abc100"],
      "timestamp": "2026-05-07T10:04:00Z"
    }
  ]
}
```

A linear history returns a `heads` array of length 1.

**404** if `(type, id)` does not exist.

**curl:**

```sh
curl http://localhost:9090/notes/abc/branches
```

---

### Fork

```
POST /:type/:id/fork
```

Creates a divergent branch starting at `from_seq`. The new branch
tip is appended as a fresh version whose only parent is the version
at `from_seq`; its `data` is `from_seq`'s snapshot byte-for-byte.
Subsequent writes against `(type, id)` extend the branch tip Fork
just produced; the original linear chain remains intact and its
prior head stays a head of the DAG.

**Request:**

```json
{"from_seq": 2}
```

**Response (201):**

```json
{
  "version_id": "v_abc100",
  "seq": 4,
  "parent_ids": ["v_abc099"],
  "timestamp": "2026-05-07T10:00:00Z"
}
```

**400** if `from_seq` is missing, non-positive, or the body is
malformed JSON. **404** if `(type, id)` does not exist. **409** if
`from_seq` is out of range (mirrors `/revert`'s error mapping for
unknown-version).

**curl:**

```sh
curl -X POST http://localhost:9090/notes/abc/fork \
  -H 'Content-Type: application/json' \
  -d '{"from_seq":2}'
```

---

### Merge

```
POST /:type/:id/merge
```

Appends a version with both source and target as parents. `data` is
the merged payload chosen by the caller; conflict detection is the
caller's job in MVP. The new version's `parent_ids` is
`[sourceVersionID, targetVersionID]` in that order.

**Request:**

```json
{
  "source_seq": 4,
  "target_seq": 3,
  "data": {"title": "merged"}
}
```

**Response (201):**

```json
{
  "version_id": "v_def789",
  "seq": 5,
  "parent_ids": ["v_abc123", "v_def456"],
  "timestamp": "2026-05-07T10:06:00Z"
}
```

**400** if any of `source_seq`, `target_seq`, `data` is missing /
invalid, or the body is malformed JSON. **404** if `(type, id)`
does not exist. **409** if either seq is out of range.

**curl:**

```sh
curl -X POST http://localhost:9090/notes/abc/merge \
  -H 'Content-Type: application/json' \
  -d '{"source_seq":4,"target_seq":3,"data":{"title":"merged"}}'
```

---

### History with Topology

```
GET /:type/:id/history?topology=1
```

Returns the full version DAG instead of the linearized list. Each
entry surfaces its `parent_ids`, plus a top-level `heads` array
listing tip version_ids. Without the query parameter the response
shape is identical to `GET /:type/:id/history` above — strict
backward compatibility for linear callers.

**Response (200):**

```json
{
  "heads": ["v_abc123", "v_def456"],
  "versions": [
    {
      "version_id": "v_abc123",
      "seq": 4,
      "parent_ids": ["v_abc100"],
      "timestamp": "2026-05-07T10:05:00Z"
    },
    {
      "version_id": "v_def456",
      "seq": 3,
      "parent_ids": ["v_abc100"],
      "timestamp": "2026-05-07T10:04:00Z"
    },
    {
      "version_id": "v_abc100",
      "seq": 2,
      "parent_ids": ["v_abc099"],
      "timestamp": "2026-05-07T10:01:00Z"
    },
    {
      "version_id": "v_abc099",
      "seq": 1,
      "parent_ids": [],
      "timestamp": "2026-05-07T10:00:00Z"
    }
  ]
}
```

Versions are listed newest-first, matching the default `/history`
shape. A linear history yields a single-element `heads` array.

**404** if `(type, id)` does not exist.

**curl:**

```sh
curl 'http://localhost:9090/notes/abc/history?topology=1'
```

---

## Pruning + Liveness

Two additive routes plus a query parameter on `/branches`. Surfaces
the prune + liveness public API on `VersionedDocumentStore` (track
`engine-version-pruning`, spec
`docs/contributors/specs/engine-version-pruning.md` §5). Schema gains an additive
`live` column on the `versions` table; existing rows take the
default (`live=true`) and existing linear callers see no behavioral
difference.

Liveness is a per-head bit. Only live heads contribute their
ancestor set to the prune retain floor, so Abandon (and the
internal Merge / Revert side-effects) is the operator-driven knob
that lets the prune algorithm actually fire on dead-subtree work.
At least one live head MUST exist for any document with history;
abandoning the last live head returns 409. Operators wanting to
drop the last live head should call `DELETE` (the document goes
away).

### Prune

```
POST /:type/:id/prune
```

Removes prunable versions per the supplied retention policy and
returns what was removed. Heads are always retained; pruning never
rewrites retained versions' `parent_ids`. A version with a retained
descendant is retained transitively (decision #3, #4).

**Request:**

```json
{
  "max_versions": 10,
  "max_age_seconds": 2592000
}
```

Either or both fields may be omitted (or set to `0`) to mean
"unlimited on that dimension." When both bounds are set, a version
must exceed BOTH to be a prune candidate (AND-rule, decision #1).

`max_age_seconds` is in whole seconds — operators rarely express
retention in nanoseconds, and the wire shape mirrors that. The
handler converts to `time.Duration` for the engine API.

**Response (200):**

```json
{
  "versions_removed": ["v_abc100", "v_abc101"],
  "blobs_freed": 2,
  "bytes_freed": 4096
}
```

`versions_removed` is in seq order (oldest first). `blobs_freed`
counts snapshot blobs whose refcount hit zero and were deleted
(blobs still referenced by other versions do not contribute).
`bytes_freed` is the sum of `len(data)` over freed blobs.

A `Prune` that finds nothing prunable returns `200` with an empty
`versions_removed` array (`[]`, not `null`), `blobs_freed: 0`,
`bytes_freed: 0`. The empty array is the no-op signal — operators
distinguish "no-op" from "policy misconfigured" via the `400`
below.

**400** if both `max_versions` and `max_age_seconds` are zero (no
policy → no-op shape would be ambiguous; explicit reject is
cleaner than a silent 200 with empty result), or if the request
body is malformed JSON. **404** if `(type, id)` does not exist.

**curl:**

```sh
curl -X POST http://localhost:9090/notes/abc/prune \
  -H 'Content-Type: application/json' \
  -d '{"max_versions":10,"max_age_seconds":2592000}'
```

---

### Abandon

```
POST /:type/:id/abandon
```

Marks the head version at `seq` as dead. Idempotent — abandoning an
already-dead head is a successful no-op.

**Request:**

```json
{"seq": 6}
```

`seq` MUST be a current head of the DAG (no children in
`version_parents`).

**Response (200):** empty body.

**400** if `seq` is missing or non-positive, or the body is
malformed JSON. **404** if `(type, id)` does not exist OR `seq`
does not exist for this document. **409** if `seq` is not a head
(`ErrNotAHead`) or is the only remaining live head
(`ErrCannotAbandonLastLiveHead`). Operators wanting to drop the
last live head should call `DELETE /:type/:id` (the document goes
away) or `Update` / `Fork` to create a new live head before
abandoning.

**curl:**

```sh
curl -X POST http://localhost:9090/notes/abc/abandon \
  -H 'Content-Type: application/json' \
  -d '{"seq":6}'
```

---

### List Branches (extended)

```
GET /:type/:id/branches
GET /:type/:id/branches?live=1
```

Default behavior unchanged from the `engine-versioned-branching`
section above: returns ALL heads (live and dead) ordered most-
recent-first. The `?live=1` query parameter filters to live heads
only — the operationally meaningful tip set after `Abandon` /
`Merge` / `Revert` are in play.

Without the parameter, dead heads appear in the result with a new
`"live": false` field on the JSON object. Live heads omit the field
entirely (default `true`, omitted for backward compat with SDK
callers that don't parse it).

**Response (200) example with one dead head:**

```json
{
  "heads": [
    {
      "version_id": "v_abc123",
      "seq": 4,
      "parent_ids": ["v_abc100"],
      "timestamp": "2026-05-07T10:05:00Z",
      "live": false
    },
    {
      "version_id": "v_def456",
      "seq": 3,
      "parent_ids": ["v_abc100"],
      "timestamp": "2026-05-07T10:04:00Z"
    }
  ]
}
```

**404** if `(type, id)` does not exist.

**curl:**

```sh
# All heads (live and dead).
curl http://localhost:9090/notes/abc/branches

# Live heads only — same shape, dead heads filtered out.
curl 'http://localhost:9090/notes/abc/branches?live=1'
```

---

## Sync

> **Status: the sync routes are a stub.** They are wired and answer with
> well-formed bodies, but no diff is stored, transmitted or replayed.
> `POST /sync/push` discards its input, `GET /sync/pull` always returns
> `[]`, and the non-identifying fields of `GET /sync/status` are
> hardcoded. The remote registry is an in-process map: it is not
> persisted and is lost on restart. Treat this section as the intended
> wire shape, not as working replication.
>
> The routes are registered only when sync is enabled. Under `--no-sync`
> or `--offline` they are absent entirely, so requests get a routing 404
> rather than an error body.

### Add Remote

```
POST /sync/remotes
```

**Request:**

```json
{
  "name": "string (required)",
  "url": "string (required, peer base URL)",
  "mode": "push | pull | both",
  "filter": "string (optional, entity type glob)"
}
```

**Response (201):**

```json
{
  "name": "peer-b",
  "url": "http://192.168.1.50:8080",
  "mode": "both",
  "filter": ""
}
```

`mode` accepts exactly `push`, `pull` or `both`. Omitted or empty
defaults to `both`; anything else is 400 `invalid remote mode`.

**400** `missing remote name or url` when either is empty.
**409** if name already exists.

**curl:**

```sh
curl -X POST http://localhost:9090/sync/remotes \
  -H 'Content-Type: application/json' \
  -d '{"name":"peer-b","url":"http://192.168.1.50:8080","mode":"both"}'
```

---

### Remove Remote

```
DELETE /sync/remotes/:name
```

**Response:** 204 No Content. Deleting an unknown name is also 204;
there is no 404 on this route.

**curl:**

```sh
curl -X DELETE http://localhost:9090/sync/remotes/peer-b
```

---

### Sync Status

```
GET /sync/status
```

**Response (200):**

Each entry echoes the four registered fields, then five status fields:

```json
{
  "remotes": [
    {
      "name": "peer-b",
      "url": "http://192.168.1.50:8080",
      "mode": "both",
      "filter": "",
      "connected": false,
      "last_sync": null,
      "pending_diffs": 0,
      "last_error": null,
      "lag_ms": 0
    }
  ]
}
```

`connected`, `last_sync`, `pending_diffs`, `last_error` and `lag_ms` are
literal constants, not live state: they always read `false`, `null`,
`0`, `null`, `0`. Entries are iterated from a Go map, so **the order of
`remotes` is nondeterministic** between calls. Sort client-side if you
need stability.

**curl:**

```sh
curl http://localhost:9090/sync/status
```

---

### Push Diffs (receive from peer)

```
POST /sync/push
```

Peer sends diffs TO this engine. Body is a JSON array of Diff
objects matching Go's `sync.Diff` struct exactly:

**Request:**

```json
[
  {
    "entity_id": "abc",
    "entity_type": "notes",
    "operation": 0,
    "before": null,
    "after": "{\"title\":\"Hello\"}",
    "timestamp": {
      "physical": 1713520000000000000,
      "logical": 1,
      "node_id": "peer-b-fingerprint"
    },
    "node_id": "peer-b-fingerprint"
  }
]
```

**Operation values:**

| Value | Meaning |
|-------|---------|
| 0     | Create  |
| 1     | Update  |
| 2     | Delete  |

**Response (200):**

```json
{"accepted": 1, "rejected": 0}
```

`accepted` is simply the length of the array you sent and `rejected` is
always `0`. The diffs are decoded, counted and discarded — nothing is
applied to the store.

**curl:**

```sh
curl -X POST http://localhost:9090/sync/push \
  -H 'Content-Type: application/json' \
  -d '[{"entity_id":"abc","entity_type":"notes","operation":0,
       "after":"{\"title\":\"Hello\"}",
       "timestamp":{"physical":1713520000000000000,"logical":1,
       "node_id":"peer-b"},"node_id":"peer-b"}]'
```

---

### Pull Diffs (serve to peer)

```
GET /sync/pull?since_physical=N&since_logical=N&since_node=S
```

Intended to return diffs since the given HLC timestamp, for peers
fetching changes they have not seen.

> **Not implemented.** The handler ignores every query parameter and
> unconditionally writes `[]`. The parameters below describe the planned
> shape; sending them changes nothing today.

**Query params (planned):**

| Param         | Type   | Required | Notes                  |
|---------------|--------|----------|------------------------|
| since_physical| int64  | yes      | UnixNano wall clock    |
| since_logical | uint32 | yes      | logical counter        |
| since_node    | string | yes      | originating node ID    |

**Response (200), planned shape:**

```json
[
  {
    "entity_id": "xyz",
    "entity_type": "notes",
    "operation": 1,
    "before": "{\"title\":\"Old\"}",
    "after": "{\"title\":\"New\"}",
    "timestamp": {
      "physical": 1713520100000000000,
      "logical": 0,
      "node_id": "local-fingerprint"
    },
    "node_id": "local-fingerprint"
  }
]
```

**Actual response today:** `[]`.

**curl:**

```sh
curl "http://localhost:9090/sync/pull?since_physical=0&since_logical=0&since_node=boot"
```

---

## Identity

### Get Identity

```
GET /identity
```

**Response (200):**

```json
{
  "public_key": "-----BEGIN PUBLIC KEY-----\n...\n-----END PUBLIC KEY-----",
  "id": "a1b2c3d4e5f67890",
  "fingerprint": "a1b2c3d4e5f67890"
}
```

**curl:**

```sh
curl http://localhost:9090/identity
```

---

### Verify Payload

```
POST /identity/verify
```

Verifies a base64-encoded Ed25519 signature against this engine's
public key.

**Request:**

```json
{
  "data": "payload string",
  "signature": "base64 signature"
}
```

**Response (200):**

```json
{
  "valid": true
}
```

**Response (200, invalid):**

```json
{
  "valid": false,
  "error": "signature mismatch"
}
```

**curl:**

```sh
curl -X POST http://localhost:9090/identity/verify \
  -H 'Content-Type: application/json' \
  -d '{"data":"payload","signature":"..."}'
```

---

## Peers

### List Peers

```
GET /peers
```

Returns all discovered peers with trust status.

**Response (200):**

```json
{
  "peers": [
    {
      "id": "a1b2c3d4e5f67890",
      "name": "laptop",
      "addrs": ["192.168.1.50:8080"],
      "trust": "trusted",
      "first_seen": "RFC3339",
      "last_seen": "RFC3339"
    }
  ]
}
```

Trust values: `unknown`, `pending_tofu`, `trusted`, `blocked`.

**curl:**

```sh
curl http://localhost:9090/peers
```

---

### Trust Peer

```
POST /peers/:id/trust
```

Promotes a `pending_tofu` or `unknown` peer to `trusted`.

**Response:** 204 No Content.

**404** if peer ID not found. **409** if peer already blocked.

**curl:**

```sh
curl -X POST http://localhost:9090/peers/a1b2c3d4e5f67890/trust
```

---

### Block Peer

```
POST /peers/:id/block
```

Sets peer to `blocked`. Blocks all sync/communication.

**Response:** 204 No Content.

**curl:**

```sh
curl -X POST http://localhost:9090/peers/a1b2c3d4e5f67890/block
```

---

## Meta

### Capabilities

```
GET /capabilities
```

Self-description of engine features. Used by SDKs to negotiate
protocol version.

**Response (200):**

```json
{
  "service": "kit-engine",
  "version": "1.0.0",
  "capabilities": [
    {"name":"endpoint:/health","type":"endpoint","path":"/health","methods":["GET"]}
  ]
}
```

**curl:**

```sh
curl http://localhost:9090/capabilities
```

---

### Health

```
GET /health
```

**Response (200):**

```json
{"status": "ok", "pid": 12345, "uptime_seconds": 3600}
```

**curl:**

```sh
curl http://localhost:9090/health
```

---

### Shutdown

```
POST /shutdown
```

Graceful shutdown. Requires the **shutdown token** specifically:
`Authorization: Bearer <shutdown_token>`. The ordinary auth token passes
the middleware but is rejected by the handler with 401 `invalid token`.

**Response:** 204 No Content. Engine process exits.

**curl:**

```sh
curl -X POST http://localhost:9090/shutdown \
  -H "Authorization: Bearer $SHUTDOWN_TOKEN"
```

---

## WebSocket: /events

Connect via WS to receive real-time events. The upgrade is a `GET`, so
it is **not authenticated** — no bearer token is required or checked.

```
ws://localhost:9090/events
```

> **Not yet delivering document events.** The engine publishes document
> mutations to the internal bus, but nothing bridges the bus to the
> WebSocket hub — `Hub.Publish` has no caller. A client today connects,
> receives `welcome`, and can subscribe and be acked, but no
> document event ever arrives. The framing below is accurate and stable;
> the delivery is not yet wired.

Defaults: 65536-byte read limit, same-origin only, 64-message send
buffer per client. Overflow drops the message rather than blocking.

### Frame format

Every frame, in both directions, is the same three-key envelope:

```json
{"type": "message", "topic": "kit.engine.document.created", "payload": {}}
```

| Key       | Type   | Notes                              |
|-----------|--------|------------------------------------|
| `type`    | string | always present                     |
| `topic`   | string | omitted when empty                 |
| `payload` | any    | omitted when empty                 |

There is no `source` or `timestamp` key on the frame.

**Server to client** `type` values:

| Value     | Meaning                                          |
|-----------|--------------------------------------------------|
| `welcome` | sent on connect; no topic, no payload            |
| `ack`     | acknowledges a subscribe or unsubscribe; echoes `topic` |
| `error`   | subscription limit (1000) exceeded; echoes `topic` |
| `message` | a broadcast; carries `topic` and `payload`       |

**Client to server** `type` values: `subscribe` and `unsubscribe`, each
with one `topic`. Unknown types and unparseable frames are ignored
silently.

### Subscribing

One topic per frame, using the same envelope — not an array:

```json
{"type": "subscribe", "topic": "kit.engine.document.*"}
```

The server replies `{"type":"ack","topic":"kit.engine.document.*"}`.

Wildcards here are **not** the bus's MQTT set. The hub matches
dot-separated segments with:

| Pattern | Matches                        |
|---------|--------------------------------|
| `*`     | exactly one segment            |
| `**`    | one or more segments           |

Note `**`, not `#`, and `**` requires at least one segment.

### Event topics

Document mutations publish these bus topics, 4-segment per the kit
convention, from source `kit.engine`:

| Topic                          | Fires when                 |
|--------------------------------|----------------------------|
| `kit.engine.document.created`  | new document inserted      |
| `kit.engine.document.updated`  | existing document modified |
| `kit.engine.document.deleted`  | document removed           |

These are the only topics `kit serve` emits. The `sync.*` and `peer.*`
topics listed in earlier drafts of this page were never implemented.

Payload is `DocumentEventPayload`:

| Key          | Type   | Notes                                |
|--------------|--------|--------------------------------------|
| `type`       | string | document type                        |
| `id`         | string | document id                          |
| `created_at` | string | omitted when empty                   |
| `updated_at` | string | omitted when empty                   |
| `version_id` | string | omitted when empty                   |
| `seq`        | int    | omitted when zero                    |

On delete only `type` and `id` are populated. Note the payload carries
`version_id` and `seq`, which the document envelope does not.

### wscat

```sh
wscat -c ws://localhost:9090/events
> {"type":"subscribe","topic":"kit.engine.document.*"}
```
