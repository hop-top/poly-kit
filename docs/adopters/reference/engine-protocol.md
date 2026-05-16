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
- Content-Type: `application/json` for all request/response bodies
- `kit serve` prints startup JSON to stdout:
  `{"port": 9090, "pid": 12345, "token": "..."}`. SDKs MUST send
  `Authorization: Bearer <token>` on mutating HTTP methods when they
  spawned the engine. Read-only `GET` and `HEAD` routes are public on
  localhost.
- Error format (all non-2xx responses):

```json
{"status": 404, "code": "not_found", "message": "document not found"}
```

| Status | Meaning             |
|--------|---------------------|
| 200    | OK                  |
| 201    | Created             |
| 204    | No Content (delete) |
| 400    | Bad Request         |
| 404    | Not Found           |
| 409    | Conflict            |
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
| limit  | int    | 100     | max items returned  |
| offset | int    | 0       | pagination offset   |
| sort   | string | created_at | `id`, `created_at`, or `updated_at` |
| search | string | —       | full-text search    |

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

**409** if concurrent write detected (optimistic locking).

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

**Response (200):** document at reverted state.

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
the branching public API on `VersionedDocumentStore` (track
`engine-versioned-branching`, spec
`docs/contributors/specs/engine-versioned-branching.md` §5). Schema is unchanged;
existing linear callers see no behavioral difference. SDK parity
(TS / Python) is gated on track `engine-sdk-protocol-reconcile` —
SDKs do not yet expose these routes.

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

**Response:** 204 No Content.

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

```json
{
  "remotes": [
    {
      "name": "peer-b",
      "connected": true,
      "last_sync": "RFC3339",
      "pending_diffs": 0,
      "last_error": null,
      "lag_ms": 120
    }
  ]
}
```

**curl:**

```sh
curl http://localhost:9090/sync/status
```

`POST /sync/remotes` and `DELETE /sync/remotes/:name` are currently
sidecar-local remote registry operations. They persist only for the
running engine process; durable sync configuration is outside the
MVP engine protocol.

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

Returns diffs since the given HLC timestamp. Peers call this
to fetch changes they haven't seen yet.

**Query params:**

| Param         | Type   | Required | Notes                  |
|---------------|--------|----------|------------------------|
| since_physical| int64  | yes      | UnixNano wall clock    |
| since_logical | uint32 | yes      | logical counter        |
| since_node    | string | yes      | originating node ID    |

**Response (200):**

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

Graceful shutdown. Flushes pending syncs, closes connections.

**Response:** 204 No Content. Engine process exits. Requires
`Authorization: Bearer <token>`.

**curl:**

```sh
curl -X POST http://localhost:9090/shutdown
```

---

## WebSocket: /events

Connect via WS to receive real-time bus events.

```
ws://localhost:9090/events
```

### Message Format

Each frame is a JSON object:

```json
{
  "topic": "document.created",
  "source": "engine",
  "timestamp": "RFC3339",
  "payload": {
    "type": "notes",
    "id": "abc",
    "data": {"title": "Hello"}
  }
}
```

### Event Topics

| Topic              | Fires when                    |
|--------------------|-------------------------------|
| document.created   | new document inserted         |
| document.updated   | existing document modified    |
| document.deleted   | document removed              |
| sync.push.start    | push cycle begins             |
| sync.push.complete | push cycle finishes           |
| sync.pull.start    | pull cycle begins             |
| sync.pull.complete | pull cycle finishes           |
| sync.conflict      | LWW conflict resolved         |
| peer.discovered    | new peer found via mDNS       |
| peer.connected     | peer handshake complete       |
| peer.disconnected  | peer connection lost          |

### Subscribing (filter)

Send a JSON frame after connecting to filter topics:

```json
{"subscribe": ["document.*", "sync.*"]}
```

MQTT-style wildcards: `*` matches one segment, `#` matches
all remaining segments.

### curl (wscat)

```sh
wscat -c ws://localhost:9090/events
> {"subscribe":["document.*"]}
```
