# Engine Sync Guide

A Go app using kit/sync natively and a TS app using kit serve
sync as equal peers. The engine speaks the same protocol as Go's
built-in replicator. Neither is client/server — both are peers.

---

## 1. Adding a Remote

Register a remote peer for sync:

```sh
curl -X POST http://localhost:9090/sync/remotes \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "go-app",
    "url": "http://192.168.1.10:8080",
    "mode": "both",
    "filter": ""
  }'
```

**Fields:**

| Field  | Type   | Required | Notes                         |
|--------|--------|----------|-------------------------------|
| name   | string | yes      | unique identifier for remote  |
| url    | string | yes      | base URL of remote peer       |
| mode   | string | yes      | `push`, `pull`, or `both`     |
| filter | string | no       | entity type glob (e.g. `notes*`) |

Remove with:

```sh
curl -X DELETE http://localhost:9090/sync/remotes/go-app
```

---

## 2. Sync Modes

| Mode   | Go Constant     | Behavior                        |
|--------|-----------------|---------------------------------|
| push   | `PushOnly`      | backup — send diffs, never pull |
| pull   | `PullOnly`      | read replica — receive only     |
| both   | `Bidirectional` | full peer — push and pull       |

**PushOnly** — one-way backup. Local changes replicate out; remote
changes never arrive. Use for archival targets.

**PullOnly** — read replica. Consumes diffs from remote; never
sends local changes. Use for dashboards/read-only views.

**Bidirectional** — equal peers. Both sides push and pull on
their configured interval. Default for peer-to-peer sync.

---

## 3. Monitoring

```sh
curl http://localhost:9090/sync/status
```

**Response:**

```json
{
  "remotes": [
    {
      "name": "go-app",
      "connected": true,
      "last_sync": "2026-04-19T10:05:00Z",
      "pending_diffs": 3,
      "last_error": null,
      "lag_ms": 85
    }
  ]
}
```

**Field meanings:**

| Field         | Meaning                                    |
|---------------|--------------------------------------------|
| connected     | transport reachable on last attempt        |
| last_sync     | timestamp of last successful push/pull     |
| pending_diffs | diffs queued but not yet sent to remote    |
| last_error    | null or last transport error string        |
| lag_ms        | ms since last successful sync              |

---

## 4. Conflict Resolution

Default strategy: **Last-Writer-Wins (LWW)** using Hybrid
Logical Clocks (HLC).

### How HLC Works

Each node maintains a Timestamp:

```json
{
  "physical": 1713520000000000000,
  "logical": 1,
  "node_id": "abc123"
}
```

- `physical` — wall clock in UnixNano, advanced on every event
- `logical` — tie-breaker when two events share physical time
- `node_id` — originating peer fingerprint (final tie-breaker)

### Resolution Rules

1. Higher `physical` wins
2. If physical equal: higher `logical` wins
3. If both equal: lexicographically greater `node_id` wins

Conflicts emit `sync.conflict` event over `/events` WebSocket
with both the winning and losing diff attached.

---

## 5. Cross-Language Scenario

Setup: Go app at `:8080`, TS engine at `:9090`. Both add each
other as remotes; create on one appears on the other.

### Step 1 — Register remotes

From the Go app (or its engine), add the TS engine:

```sh
curl -X POST http://localhost:8080/sync/remotes \
  -H 'Content-Type: application/json' \
  -d '{"name":"ts-peer","url":"http://localhost:9090","mode":"both"}'
```

From the TS engine, add the Go app:

```sh
curl -X POST http://localhost:9090/sync/remotes \
  -H 'Content-Type: application/json' \
  -d '{"name":"go-peer","url":"http://localhost:8080","mode":"both"}'
```

### Step 2 — Create document on Go side

```sh
curl -X POST http://localhost:8080/notes/ \
  -H 'Content-Type: application/json' \
  -d '{"data":{"title":"From Go","body":"synced"}}'
```

### Step 3 — Verify on TS side

After sync interval (default ~100ms):

```sh
curl http://localhost:9090/notes/
# -> includes the "From Go" document
```

### Step 4 — Create on TS side

```sh
curl -X POST http://localhost:9090/notes/ \
  -H 'Content-Type: application/json' \
  -d '{"data":{"title":"From TS","body":"also synced"}}'
```

Appears on Go side within next sync cycle.

---

## 6. Diagrams

### Two-Peer Sync

```
  ┌──────────┐                    ┌──────────┐
  │  Go App  │                    │ TS Engine│
  │  :8080   │                    │  :9090   │
  └────┬─────┘                    └────┬─────┘
       │                               │
       │──── POST /sync/push ─────────>│  (push diffs)
       │                               │
       │<─── GET /sync/pull ───────────│  (pull diffs)
       │                               │
       │  (interval repeats both dirs) │
       │                               │
```

### Three-Way with Relay Server

```
  ┌──────────┐         ┌──────────┐         ┌──────────┐
  │  Peer A  │         │  Relay   │         │  Peer B  │
  │  :8080   │         │  :7070   │         │  :9090   │
  └────┬─────┘         └────┬─────┘         └────┬─────┘
       │                     │                     │
       │── push diffs ──────>│                     │
       │                     │<── pull diffs ──────│
       │                     │                     │
       │                     │── push diffs ──────>│
       │<── pull diffs ──────│                     │
       │                     │                     │
```

Relay runs `kit serve` with `mode: both` to all peers. Acts as
a dumb forwarder — no special relay logic needed.

### Partition Recovery

```
  Timeline:
  ─────────────────────────────────────────────────────

  t0: A and B in sync
      A: [d1, d2, d3]    B: [d1, d2, d3]

  t1: Network partition — A and B diverge
      A: [d1, d2, d3, d4]    B: [d1, d2, d3, d5]

  t2: Partition heals — sync resumes
      A pulls from B: gets d5 (HLC > cursor)
      B pulls from A: gets d4 (HLC > cursor)

  t3: Both converged
      A: [d1, d2, d3, d4, d5]    B: [d1, d2, d3, d4, d5]

  If d4 and d5 modify same entity: LWW resolves via HLC.
```

---

## 7. Troubleshooting

### High `lag_ms`

- Check network connectivity to remote URL
- Verify remote is running (`GET /health` on remote)
- Reduce sync interval if real-time needed

### Growing `pending_diffs`

- Remote unreachable — diffs queue locally
- Check `last_error` in status response
- Once remote recovers, pending diffs flush automatically

### Connection Errors

| Error                  | Cause                       | Fix                      |
|------------------------|-----------------------------|--------------------------|
| `connection refused`   | remote not running          | start remote engine      |
| `timeout`             | network/firewall            | check routing            |
| `401 unauthorized`    | peer not trusted            | trust peer on remote     |
| `pubkey mismatch`     | impersonation or key rotated| re-register peer         |

### Stale Data After Partition

If peers diverged during partition and you see unexpected
old values:

1. Check `GET /sync/status` — confirm `connected: true`
2. Wait for `pending_diffs` to reach 0
3. If LWW picked wrong winner: manually update the document
4. Monitor `sync.conflict` events for visibility
