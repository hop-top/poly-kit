# Bus network streams

Status: accepted. Ships in the same kit release as CloudEvents framing, so
no peer generation decodes CloudEvents frames without being able to
advertise it.

Defines stream semantics for the bus `NetworkAdapter`
([`go/runtime/bus`](../../../go/runtime/bus/README.md)): capability
advertisement, link liveness and link state, remote subscribe, resume
from an offset, the gap signal, an offline outbox, and acknowledged
publishes from a dialer to an acceptor. Go is canonical.
This page and the vectors under `contracts/bus-v1/streams/` are the
cross-SDK contract.

## Context

Before streams, the network layer is a symmetric, best-effort fan-out:

- every peer forwards every local event (`#`, minus `TopicFilter`) to
  every connected peer; there is no per-connection subscription
- frames carry no position, so a peer that reconnects cannot ask for what
  it missed, and nothing tells it that it missed anything
- an event published while a peer is disconnected is dropped
- forwarding runs in a `SubscribeAsync` handler, one goroutine per event,
  so even a single publisher's events can leave out of order
- the only handshake is the optional auth frame and its `auth_ok` ack;
  without an `Authenticator` the first frame is already an event
- any JSON object that is not a CloudEvents frame decodes as a kit frame.
  A peer that predates streams would publish an unknown control frame on
  its local bus as an event with an empty topic (accepted in `ModeWarn`)
  and, as a relay, forward it further
- nothing crosses an idle link, so a half-open socket looks live forever

A networked consumer (a desktop or mobile shell talking to a core hub,
possibly from a browser) needs: subscribe by topic pattern and workspace
scope; resume after reconnect with no gaps and no duplicates; an explicit
signal whenever that is impossible, so it can mark live views stale and
refetch instead of showing stale data as live; a link-state signal for
"reconnecting" notices; dead-link detection that works where WebSocket
pings are invisible; and publishes made while offline kept in order.

## Goals and non-goals

Goals:

- one wire contract, additive on top of the existing frames
- peers that predate streams keep working byte for byte, and never
  receive a frame they would misread
- ordered delivery per sending adapter; resume without gaps or duplicates
  within one stream; an explicit gap signal whenever that breaks
- frame format chosen per link from what the peer advertises
- a core that is pure and clock-free: classification, cursor rules,
  resume decision, filters, offsets, outbox and ack tracking are
  functions a port can test without sockets or timers
- acknowledged publishes from a dialer to an acceptor, negotiated per
  link (`caps.acks`), so a publisher can count a peer's handling as
  delivery (section 13)

Non-goals for streams v1:

- a durable log ships later; v1 defines the log seam and ships a ring
- exactly-once delivery end to end: acknowledged publishes are at least
  once, deduplicated by the acceptor while it runs (section 13)
- acknowledged publishes from an acceptor to its dialers: the `puback`
  frame is direction-agnostic, but v1 negotiates it dialer to acceptor
  only
- a relay subscribing upstream on behalf of its downstream peers
- per-tenant policy: kit provides scope, a per-link filter and an
  authorization hook; the hub decides

## Terms

| Term | Meaning |
|------|---------|
| link | one WebSocket connection between two adapters |
| dialer / acceptor | the side that called `Connect` or `Dial` / the side serving `Handler` |
| stream | the ordered sequence of entries one adapter sends; identified by a stream id |
| log | the adapter's bounded store of the recent part of its stream (`StreamLog`) |
| entry | one log record: an event, or a gap marker (section 8) |
| offset | an entry's position in its stream: `1, 2, 3, …`, assigned once at append |
| position | `(stream id, offset)` of the last entry a dialer handled from one link |
| stream link | a link on which both sides advertised `streams >= 1` |
| legacy link | any other link; behaves as before, except where section 12 says |

## 1. Log and ordering

### 1.1 One log per adapter, one cursor and writer per link

Every adapter keeps a log of the entries it forwards, in the order it
accepted them. An event enters the log when the adapter forwards it: a
local publish or, in relay mode only, an inbound network event.
`TopicFilter` applies before the log, and reserved topics (section 7.3)
never enter it. Each entry holds the event, its wire origin and its
offset.

Each link has a cursor into the log and one writer goroutine. The writer
sends entries after the cursor in offset order, applying the link's
filters (section 6). It also sends every control frame for that link
(`ack`, `error`, `reset`, `position`), so replies and events share one
ordered sequence. Replay and live delivery are the same loop: a resumed
link starts with its cursor in the past, a fresh one at the head.

### 1.2 The log seam

```go
type StreamLog interface {
    ID() string                                 // stream id, see 3.1
    Append(e LogEntry) uint64                   // assigns and returns the offset; must not block
    ReadAfter(after uint64, max int) []LogEntry // entries with offset > after, ascending, at most max
    Oldest() uint64                             // first retained offset; Head()+1 when empty
    Head() uint64                               // last assigned offset; 0 before the first append
}
```

v1 ships `NewRingLog(id string, size int)`. The default is a ring of 1024
entries with a fresh stream id. `WithStreamLog(size)` resizes it, and
`WithLog(StreamLog)` plugs in another implementation. A hub backed by a
durable event log can keep its stream id across restarts, which turns a
hub restart from a reset into a replay. Size a hub's ring for its
clients' reconnect window; 1024 suits peer meshes, not mobile clients
that sleep (10 000 is a reasonable hub figure). A byte bound is a later
addition.

Entries are dense. A reader that gets a first entry above `after + 1`
knows the entries in between were evicted. That is how lag is detected,
without a race between `Oldest` and `ReadAfter`.

### 1.3 Ordered, veto-aware append

The append must happen in publish order and only for events the bus
accepted (no sync handler vetoed them). The bus core provides that hook:

```go
type Observer func(ctx context.Context, e Event)

type PublishObserver interface {
    ObservePublished(o Observer) Unsubscribe
}
```

The memory bus calls observers in the publishing goroutine after every
sync handler has returned nil, before async handlers start, and while
still holding its read lock. An observer must not block and must not
publish. The adapter's observer performs an O(1) append and a wake-up.
Concurrent publishers are ordered by whoever reaches the log first, which
is a total order consistent with each goroutine's program order.

The adapter finds the hook through `PublishObserver`, or by unwrapping
kit's own wrappers: the bus returned by `New` with a network or
`WithAdapter(NewMemoryAdapter())`, and `TeeBus`. A `Bus` without the hook,
such as the SQLite adapter, keeps the previous async forwarder, which
appends from an async handler: veto-aware, but unordered. Such an adapter
does not advertise `streams`. It never falls back to a sync subscriber,
which would forward events that a later sync handler vetoed.

## 2. Handshake and capabilities

### 2.1 Frames

Capabilities ride the existing auth exchange. Every new key is ignored by
a peer that predates streams. `Connect` decodes only the ack's `type`, and
`Handler` decodes only the auth frame's `auth`.

Dialer to acceptor, first frame:

```json
{
  "auth": "s3cr3t",
  "caps": {"streams": 1, "frames": ["kit", "cloudevents"]},
  "origin": "a1b2c3d4e5f60718",
  "session": {
    "subscribe": ["acme.crm.#", "acme.jobs.*.failed"],
    "scope": {"workspaces": ["ws1"]},
    "resume": {"stream": "stm_01jg000000e00a000000000000", "after": "41"}
  }
}
```

Acceptor to dialer, when the dialer sent `caps`:

```json
{
  "type": "auth_ok",
  "caps": {"streams": 1, "frames": ["kit", "cloudevents"]},
  "origin": "0f1e2d3c4b5a6978",
  "stream": {"id": "stm_01jg000000e00a000000000000", "oldest": "17", "head": "57", "heartbeat_ms": 15000},
  "session": {"resume": {"status": "replaying", "after": "41", "head": "57"}}
}
```

When the dialer sent no `caps`, the acceptor answers with exactly
`{"type":"auth_ok"}`, so the legacy wire stays byte for byte.

A session the acceptor refuses gets a distinct reply instead of an ack,
then a close with 1008 (`invalid session`):

```json
{"type": "session_error", "code": "forbidden", "message": "workspace ws9 not allowed"}
```

### 2.2 Keys

| Key | Rule |
|-----|------|
| `caps.streams` | integer protocol version; absent or `0` means none. The link version is the minimum of both sides. v1 is `1`. An adapter whose bus lacks the publish hook advertises `0` |
| `caps.frames` | formats this peer decodes, from `kit` and `cloudevents`; unknown names are ignored |
| `origin` | the sender's loop-prevention origin id, known before the first event |
| `stream.id` | acceptor's stream id, see 3.1 |
| `stream.oldest`, `stream.head` | offsets as in 1.2, decimal strings |
| `stream.heartbeat_ms` | acceptor's heartbeat interval in 1 000..86 400 000 (one second to one day), see 4.1 |
| `session` | dialer's initial subscription, scope and resume request, applied atomically before the link is registered, so no event slips between ack and replay |
| `session.resume` (ack) | outcome, see 3.3; absent when the dialer asked for no resume |
| `caps.acks` | acknowledged-publish version, see 13; absent or `0` means none. A dialer sends it only when built with `WithAcks`; an acceptor echoes it only when the dialer sent it, so other acks keep their bytes |
| `stream.id` (auth frame) | the dialer's own stream id, required with `caps.acks` (else `session_error` `bad_request`), so the acceptor can recognize frames sent again after a reconnect |

Forward compatibility:

- unknown members of the auth frame, ack, `caps`, `session`, `stream`
  and control frames are ignored
- `session` is sent before the version is known, so an acceptor ignores
  `session` keys it does not know
- a dialer relies on a vN feature only once the negotiated
  `caps.streams >= N`; in particular it requires `stream` in the ack
  only when min(own, acceptor's) `caps.streams >= 1`, so a dialer that
  advertises 0 connects to a v1 acceptor as a legacy link
- a dialer checks what it can check, and any failure is a protocol
  error: offsets are decimal tokens of at most 2^64-1; `heartbeat_ms`
  is in 1 000..86 400 000; `oldest <= head + 1`; a `replaying` outcome
  repeats the requested `after` and does not exceed `stream.head`; a
  `reset` outcome's `head` does not exceed `stream.head`; an outcome
  without a request is refused; a `reset` frame has `after <= head` and
  `head` at or above the position. Live event offsets and `position`
  offsets have no upper bound the dialer can check (the acceptor's log
  keeps growing), so they are trusted: an acceptor that sends a huge
  offset mutes its dialer until the next reset, the same harm as an
  acceptor that withholds events

### 2.3 Auth is the handshake

Streams need the handshake, and the handshake exists only when both
sides configure an `Authenticator`. Without one, every link is legacy.
For deployments without a secret, `OpenAuth()` returns an authenticator
whose token is the fixed string `open` and whose `Verify` accepts any
non-empty token. (`Handler` rejects an empty `auth`.) A handshake that
works without auth (a hello frame shaped so older peers drop it) is a
candidate for streams v2.

Only the dialer sends `session`. Stream semantics (subscribe, resume,
heartbeat, link state) flow from acceptor to dialer, which is hub to
client. Both sides stamp offsets on the events they send over a stream
link. The dialer's own stream reaches the acceptor through the outbox
(section 9), without resume.

## 3. Offsets, resume and gaps

### 3.1 Offsets and stream ids

- offsets are assigned by the sending adapter at log append, never by
  the publisher, and never carried over from an upstream hop
- they are unsigned 64-bit, start at 1, and are dense (+1 per entry)
  within a stream
- they count per stream, not per topic. A receiver sees holes where its
  filters drop entries, so density cannot detect loss; resume is what
  guarantees no loss
- on the wire, an offset is a decimal string with no sign, no leading
  zeros and at most 2^64-1 (`"0"`, `"42"`, `"18446744073709551615"`).
  JSON numbers lose precision above 2^53, and CloudEvents integers stop at
  2^31-1. Receivers may compare two offsets of one stream as unsigned
  integers, and nothing else
- a stream id is an opaque ASCII string of 1 to 128 bytes, compared by
  byte equality only and never parsed. The ring log mints a TypeID with
  prefix `stm` when the adapter starts, so a restarted adapter has a new
  stream and old offsets cannot be mistaken for new ones. A durable log
  may keep its id across restarts

Kit event frames on a stream link carry `offset`. CloudEvents frames
carry the extension `kitoffset`, a string. On every link, stream or
legacy, both are stripped on decode and never reach subscribers. A
legacy link never rejects a frame because of them. `kitoffset` is
stripped before the reserved-extension check, as `kitorigin` is, so a
frame is never rejected for carrying it.

### 3.2 Delivery metadata

The position is delivery metadata, not part of the event. `Event` stays
a pure envelope. Go handlers read it from their context:

```go
func StreamPositionFrom(ctx context.Context) (StreamPosition, bool)
type StreamPosition struct{ Peer, Stream string; Offset uint64 }
```

It is present only for events that a dialer received over a stream link.
Ports expose the same values through their handler context (kit-rs:
`bus::Context`). Vectors pin the values, not the accessor. A consumer
that carries offsets in its own CloudEvents extension maps them from
there; the extension must be a string, since CloudEvents integers stop
at 2^31-1.

### 3.3 Resume decision (acceptor)

`session.resume = {stream, after}` asks for every entry with an offset
greater than `after`. `after: "0"` asks for the whole stream. No `resume`
means start live at the head. Given its log `(S, oldest O, head H)` and
the request `(s, a)`, the acceptor decides in this order:

| Condition | Outcome | Cursor |
|-----------|---------|--------|
| no `resume` | none (key absent) | `H` |
| `s != S` | `reset`, reason `stream` | `H` |
| `a > H` | `reset`, reason `ahead` | `H` |
| `a < O - 1` | `reset`, reason `evicted` | `H` |
| otherwise | `replaying` (`a == H` replays nothing) | `a` |

A reset outcome in the ack:

```json
{"type": "auth_ok", "caps": {"streams": 1, "frames": ["kit", "cloudevents"]}, "origin": "0f1e2d3c4b5a6978", "stream": {"id": "stm_01jg000000e00a000000000000", "oldest": "17", "head": "57", "heartbeat_ms": 15000}, "session": {"resume": {"status": "reset", "reason": "evicted", "after": "5", "head": "57"}}}
```

When the writer finds its cursor below the oldest entry (a stalled
receiver), it sends an in-band `reset` with reason `lagged` and jumps to
the head. The link stays up.

```json
{"type": "reset", "stream": "stm_01jg000000e00a000000000000", "reason": "lagged", "after": "120", "head": "1300"}
```

Reset reasons: `stream`, `ahead`, `evicted`, `lagged`, `upstream`
(section 8). A dialer also reports `unsupported` locally (3.5).

### 3.4 Dialer cursor rules

These rules are normative, and `cursor.json` pins them.

1. The position is `(stream, offset)`, plus whether one is held. Each
   outbound address has one position, and it survives reconnects.
2. On an ack with `stream.id = S`:
   - `replaying`: position := `(S, after)`. If the held position names
     a stream other than `S`, the ack is treated as a reset with reason
     `stream`: position := `(S, stream.head)`
   - `reset`: position := `(S, head)`, and the dialer publishes
     `invalidated` with the ack's reason
   - no resume outcome: position := `(S, stream.head)`
3. On an event frame:
   - a missing or malformed offset is a protocol error
   - an offset `<= position.offset` is dropped as a duplicate
   - otherwise the event is delivered and position := `(S, offset)`

   All frames on a link belong to the link's `S`, so this dedupe never
   compares across streams.
4. On a `reset` frame naming `S`: position := `(S, head)`, and the dialer
   publishes `invalidated`.
5. On a `position` frame naming `S`: position.offset := `max(position.offset, offset)`.
   A `reset` frame whose `head` is below position.offset is a protocol
   error: offsets only grow.
6. A `reset` or `position` frame naming a stream other than `S` is a
   protocol error.
7. A protocol error closes the link with 1002 (`protocol error`), which
   triggers reconnect and resume.
8. A link that comes up without `replaying` while a resume was wanted
   makes the dialer publish `invalidated` before anything else. A resume
   is wanted when a position is held or the dialer is reconnecting. This
   covers:
   - an ack with reset (rule 2)
   - a legacy ack, or a link with no handshake (reason `unsupported`)
   - a stream link that answers a reconnect with no resume outcome
     (reason `unsupported`)

   A legacy link clears the held position.
9. The position advances when the dialer's sync handlers return, whether
   or not `Publish` returned an error. A veto is a local decision, not a
   delivery failure.

`WithResumeFrom(addr, StreamPosition)` seeds a position, for a process
that persisted one. `(*NetworkAdapter).Position(addr)` reads it. A
consumer that queues events onward (to actors, through a channel) must
persist the position it actually applied, read from
`StreamPositionFrom` in its apply path, not `Position(addr)`, which can
run ahead of applied state.

### 3.5 The gap signal

The dialer publishes one local event per gap, in order with the stream,
before anything that follows the gap:

- topic `kit.runtime.stream.invalidated`
- source `kit.bus.network`
- payload `{"peer", "stream", "reason", "after", "head"}`. `peer` is the
  address this dialer connected to. Offsets are decimal strings, and
  `stream`, `after` and `head` are empty for `unsupported`

Consumer contract: on `invalidated`, mark every live view fed by that
peer stale and refetch it. Events that follow are live. `head` says how
far behind the consumer is. A consumer with its own versioned log can
catch up from that log instead of refetching. When a hub restarts, every
client resets at once, so consumers should jitter their refetches.

### 3.6 Inbound bound (dialers without backpressure)

A dialer bounds its inbound queue. A browser drains the socket whatever
the application's speed, so the acceptor never sees backpressure and
never lags such a link. On overflow, the dialer:

- discards what it queued but did not deliver
- closes the link
- reconnects with resume from its last delivered position

That heals without a gap while still inside the acceptor's log window.
Only an `evicted` outcome becomes an invalidation. The Go dialer
delivers synchronously from its read loop: a queue of one, with
backpressure through TCP. `inbound.json` pins the overflow-then-resume
rule for ports that queue.

## 4. Liveness and link state

### 4.1 Heartbeat

On a stream link the acceptor's writer handles a tick every
`heartbeat_ms` (default 15 000, `WithHeartbeat`, 1 000 to 86 400 000). On each tick it sends:

```json
{"type": "position", "stream": "stm_01jg000000e00a000000000000", "offset": "1450"}
```

whenever no frame was written since the previous tick, or its cursor is
past the last offset it sent. The same frame keeps quiet subscriptions
from a spurious `evicted`: filtered-out entries do not move the dialer's
position, but `position` does.

The dialer counts its own ticks of `heartbeat_ms` (taken from the ack).
After three ticks in a row with no frame of any kind, it closes the
link (code `timeout`), then reconnects with resume. The frame is
application level, not a WebSocket ping, because browser JavaScript can
neither send pings nor see pongs. Timers live in the transport shell and
only drive `tick()`. The rules themselves are clock-free, and so are the
vectors. Legacy links get no heartbeat.

### 4.2 Link-state events

For each outbound address, the dialer publishes local events in order
with the stream:

| Topic | When | Payload |
|-------|------|---------|
| `kit.runtime.link.connected` | after the handshake, before `invalidated` and any replayed event | `{"peer", "stream", "resume": "none"\|"replaying"\|"reset"\|"unsupported", "format": "kit"\|"cloudevents"}` |
| `kit.runtime.link.disconnected` | after the last delivered event of a link | `{"peer", "final": bool, "code"?}` |

`resume` is `unsupported` on any legacy link. `final` is true when the
adapter will not reconnect: an explicit `Disconnect`, or a session error.
`code` is one of the following:

- a session error code
- `protocol`: closed with 1002
- `timeout`: heartbeat
- `auth`: the handshake failed

`(*NetworkAdapter).LinkState(addr)` returns the same facts.

`invalidated` stays the one signal that means "you have a gap". The link
events carry state only. Acceptors publish no link events for inbound
links.

## 5. Frame format per link

A sender picks per link:

- no handshake (no auth): the configured `FrameFormat`, as before; that
  is an explicit opt-in
- handshake: the configured format if the peer listed it in
  `caps.frames`, otherwise `kit`; a peer that sent no `caps` lists
  nothing

The default stays `FrameKit` in every SDK. Receivers accept both
formats on every link, detected by the exact `specversion` key. The
negotiated format appears in `LinkState` and in `link.connected`, so a
consumer that depends on CloudEvents extensions can detect the
downgrade. A kit hop loses `subject`, `datacontenttype`, `dataschema` and
extensions.

## 6. Remote subscribe

### 6.1 Subscriptions and scope

Stream links only, from dialer to acceptor.

**Initial subscription.** A link starts with the implicit subscription
`#`, the previous behaviour. The first explicit subscription, from
`session.subscribe` or a `subscribe` frame, replaces the implicit one.
`session.subscribe: []` means subscribed to nothing yet.

**Patterns.** A link's subscriptions are a set of patterns (subscribing
twice is a no-op), capped at 1000 (`WithMaxSubscriptions`). A pattern is
valid when:

- it is non-empty and at most 256 bytes
- every `.`-separated segment is non-empty
- `*` and `#` appear only as whole segments
- `#` appears only as the last segment

Matching is `Topic.Match`.

**Unsubscribe.** Unsubscribing while the implicit `#` is active first
turns it into the explicit set `{#}`, then removes the patterns. So
`unsubscribe ["#"]` empties the set, and removing another pattern
changes nothing.

**Scope.** Scope is per link: `{"workspaces": [...]}`.

- An event passes when its `workspace_id` is in the list, or it has
  none: global events always pass.
- `"workspaces": []` passes global events only.
- No scope means all workspaces.

A `subscribe` that carries `scope` replaces the link's scope. A
`subscribe` with `scope` and no `patterns` changes only the scope.

Subscription changes take effect from the writer's cursor forward.
Adding a pattern never replays history.

### 6.2 Authorization and per-link filters

`WithSubscribeAuthorizer(func(ctx, Peer, Subscription) error)` sees every
effective subscription: the set and scope the link would have after the
request. It is called:

- at handshake on stream links, including the implicit `#` and an
  absent scope (all workspaces)
- at accept time on legacy links, with `Subscription{Patterns: ["#"]}`,
  with or without an `Authenticator`
- on every `subscribe` frame. Unsubscribing only narrows and is not
  authorized

A refusal is `forbidden`:

- handshake on a stream link: `session_error`
- legacy link: close 1008 (`forbidden`) without an ack
- `subscribe` frame: an `error` reply

`Peer` carries the remote address, the advertised origin and the token
the link presented.

`WithLinkFilter(func(Peer, Event) bool)` is a per-link event predicate,
applied on replay and live, on every link. With it, a hub can:

- hide global events from some links
- isolate by a tenant key other than `workspace_id`

`TopicFilter` stays adapter-wide.

A writer delivers an event entry when all of these hold, in this order:

1. its origin differs from the peer's origin
2. it matches the subscription set
3. it passes the scope
4. it passes the link filter

Gap markers (section 8) skip all four.

### 6.3 Snapshot plus tail

A subscribe or unsubscribe `ack` carries `at`, the writer's cursor when
the new set took effect. Every event after `at` is filtered by the new
set. To build a consistent view:

1. subscribe, and buffer incoming events
2. wait for `ack{at}`
3. fetch the snapshot with its application-level version
4. apply the buffered and later events whose version is above the
   snapshot's

## 7. Control frames

### 7.1 Classification

A frame is classified in this order:

1. not a JSON object: dropped
2. has a member named exactly `specversion`: a CloudEvents event
3. on a stream link, has a string member `type`: a control frame
4. otherwise: a kit event

Kit event frames never carry `type`. Control frames have the same shape
under either frame format; they are never CloudEvents. A peer never sends
a control frame over a legacy link.

### 7.2 Types

| `type` | Direction | Members | Reply |
|--------|-----------|---------|-------|
| `subscribe` | dialer to acceptor | `id`, `patterns`?, `scope`? (at least one) | `ack` or `error` |
| `unsubscribe` | dialer to acceptor | `id`, `patterns` | `ack` |
| `ack` | acceptor to dialer | `id`, `at` | none |
| `error` | acceptor to dialer | `id`?, `code`, `message` | none |
| `reset` | acceptor to dialer | `stream`, `reason`, `after`, `head` | none |
| `position` | acceptor to dialer | `stream`, `offset` | none |
| `puback` | acceptor to dialer, on an acks link | `offset`, `status`, `id`?, `code`?, `message`? | none |

```json
{"type": "subscribe", "id": "s2", "patterns": ["acme.billing.invoice.*"], "scope": {"workspaces": ["ws1", "ws2"]}}
{"type": "ack", "id": "s2", "at": "1450"}
{"type": "subscribe", "id": "s3", "scope": {"workspaces": ["ws3"]}}
{"type": "unsubscribe", "id": "u1", "patterns": ["acme.crm.#"]}
{"type": "error", "id": "s4", "code": "invalid_pattern", "message": "segment 2: \"#\" must be the last segment"}
{"type": "error", "code": "unknown_type", "message": "unknown control frame type \"resume\""}
```

`id` is chosen by the dialer: a non-empty string of at most 64 bytes,
echoed in the reply. Requests are processed in order. A request is all or
nothing.

Each side ignores some frames:

- an acceptor ignores `ack`, `error` and `puback`, and answers `reset`
  and `position` with `bad_request`
- a dialer answers nothing, and ignores `subscribe` and `unsubscribe`

Error codes are contract; messages are not.

| Code | When |
|------|------|
| `bad_request` | missing or ill-typed member; empty or oversized `id`; `patterns` empty or not an array of strings; neither `patterns` nor `scope`; a wrong-direction frame |
| `invalid_pattern` | a pattern breaks 6.1 |
| `invalid_scope` | `scope` is not an object whose `workspaces` is an array of strings |
| `limit` | the result would exceed the subscription cap |
| `forbidden` | the authorizer refused |
| `unknown_type` | `type` is not in the table |

The same codes apply to `session` in the handshake (`session_error`).
`Connect` returns `*StreamError{Code, Message}` (`errors.Is(err,
ErrStream)`). The reconnect loop treats a session error as permanent and
stops. The dialer publishes `link.disconnected{final: true, code}` and
reports the error through `WithNetworkErrorFunc`.

### 7.3 Reserved topics

The adapter owns `kit.runtime.stream.*` and `kit.runtime.link.*`:

- these topics never enter a log
- inbound network frames carrying them are dropped, so a remote peer can
  neither forge an invalidation nor trigger refetch storms
- the adapter publishes them locally with the network-origin marker,
  which keeps them out of forwarding

## 8. Relay

A relay logs inbound events into its own stream with its own offsets. An
upstream offset is stripped on decode and never forwarded, so each hop's
offsets belong to the adapter that sent the frame. A client of relay B
resumes against B's stream. B, as a dialer of hub A, resumes against A's.

Origin exclusion runs in the per-link filter. A relay knows each
peer's origin from the handshake, or learns it from the first frame on a
legacy link. It skips that peer's own entries on replay as well as live.

When a relay publishes `invalidated` for one of its upstream links, it
also appends a gap marker to its log. A marker:

- passes `TopicFilter`, subscriptions, scope and the link filter
- reaches every stream link as a `reset` with reason `upstream`, ordered
  by offset, on replay and live
- carries `after` = its offset − 1 and `head` = its offset
- is skipped on legacy links

Each downstream dialer publishes its own `invalidated` naming its own
peer. The upstream address never leaves the relay, and
`kit.runtime.stream.invalidated` is never relayed as an event.

Hub replicas behind one address need sticky sessions. Without them, each
reconnect lands on another replica's stream and resets.

## 9. Offline outbox (dialer)

The outbox is opt-in: `WithOutbox()`. The bus stays "not a queue" by
default.

The outbox is the dialer's own log seen through a disconnected outbound
address's send cursor. The cursor survives the disconnect, and on
reconnect the writer resumes from it, so queued events go out in publish
order, before any later event.

A write that fails closes an outbox link and rewinds the send cursor to
that frame, so the next link sends it first. If the frame's bytes had
reached the peer before the error (a write timeout, say), the peer gets
it twice; dedupe by event `id`. A frame that was written successfully
but lost with the connection is not resent, unless the link negotiated
acks: then the next link starts after the last acknowledged offset
(section 13).

- **start:** for an address given to `WithNetwork` or `Dial`, the outbox
  starts when the adapter registers it, not at the first successful
  connect. Events made during an offline start are kept. A plain
  `Connect` starts it at its first success. It ends at `Disconnect(addr)`
  or `Close`
- **bound:** the log size. Entries evicted before a send cursor reached
  them are dropped oldest first
- **counter:** `(*NetworkAdapter).OutboxDropped(addr) uint64`, cumulative
  per address. `WithOutboxDropFunc(func(addr string, n uint64))` reports
  each batch when the flush finds it
- **legacy links:** the outbox works on them too, because it is purely
  sender-side
- **off:** a reconnect starts the cursor at the head, as before

`New(WithNetwork(addrs...))` and `Dial(addr)` keep retrying a failed
first dial, so an adapter that starts offline connects once the network
returns. A plain `Connect` still returns its error and does not retry
until it has succeeded once.

## 10. Delivery guarantees

| Path | Guarantee |
|------|-----------|
| one link, while up | each entry sent at most once, in offset order |
| acceptor to dialer, across a reconnect, same processes | no gaps and no duplicates for the subscription, or an explicit `invalidated` |
| acceptor to dialer, dialer restarted with a persisted position | at least once: entries after the persisted position are replayed; dedupe by `(stream, offset)` or event `id` |
| acceptor restarted, ring log | no replay: new stream id, reset with reason `stream` |
| dialer to acceptor, outbox on | queued events in order; a frame whose write failed is resent on the next link (a duplicate if its bytes had already reached the peer); a frame that was written but lost in transit is not resent; overflow drops oldest, counted |
| dialer to acceptor, outbox off | the previous behaviour |
| dialer to acceptor, acks link | at least once: everything after the last acknowledged offset is sent again on the next link; the acceptor publishes each frame of a dialer stream once while it runs and answers copies with the first copy's result; after an acceptor restart, dedupe by event `id` |
| local handlers | per-sender order holds through **sync** handlers only; async handlers run one goroutine each and stay unordered |
| end to end | no exactly-once; a sync handler veto does not hold back the position |

Two publishers in different processes have no relative order beyond the
order in which their events enter a common relay's log.

## 11. Separate from the API hub

`go/transport/api` also has `{"type":"subscribe","topic":…}` frames, on
its own endpoint, with its own semantics. That is a different protocol.
The bus network streams take its cap of 1000 subscriptions and its
ack/error shape, but the two may converge later and do not share code.

## 12. Backward compatibility

| Pair | Result |
|------|--------|
| new dialer, legacy acceptor | plain ack, so the link is legacy: no offsets, no control frames, format `kit` unless no auth. The outbox still works. If a resume was wanted, `invalidated` with reason `unsupported` |
| legacy dialer, new acceptor | no `caps`, so the acceptor sends the plain ack and the link is legacy: implicit `#`, frames as before |
| new peers, no `Authenticator` | legacy link, frames as before; a reconnect (or a seeded position) publishes `invalidated` with reason `unsupported` |
| new peers, auth, nothing configured | stream link with implicit `#`: the same events as before, plus `offset`, heartbeats and link events |
| CloudEvents frames vectors | unchanged: control-frame classification applies only on stream links; `offset`/`kitoffset` stripping never rejects |
| acks dialer, acceptor without acks | no `caps.acks` in the ack, so no pubacks: every acknowledged publish for that address fails with `unsupported` |
| dialer without acks, new acceptor | no `caps.acks` sent, none echoed, no `puback` frames; the ack's bytes are unchanged |

Behaviour changes for existing adopters (listed in the bus API reference and in the commit bodies):

- forwarding is ordered per sender, through per-link writers instead of
  one goroutine per event
- a slow legacy link skips evicted entries instead of back-pressuring
  publishers. The skips are counted: `(*NetworkAdapter).Skipped(addr)`
  and `WithSkipFunc(func(addr string, n uint64))`, also reported through
  `WithNetworkErrorFunc`
- `FrameCloudEvents` falls back to kit frames on a handshake link whose
  peer did not advertise `cloudevents`. Links without a handshake keep
  the configured format
- `kitoffset` is a reserved CloudEvents extension, like `kitorigin`. It is
  an exact name, not a `kit` prefix: extension names have no separator,
  so a prefix would also reserve ordinary names such as `kitchen`
- a failed first dial through `WithNetwork` now retries
- an acceptor ignores `puback` frames instead of answering
  `unknown_type`

## 13. Acknowledged publishes

`Publish` returning nil confirms local sync handlers only. An
acknowledged publish also waits for local async handlers and for each
awaited outbound address to report that its acceptor handled the
event.

### 13.1 Negotiation

A dialer built with `WithAcks()` advertises `caps.acks: 1` and its own
`stream.id` in the auth frame. An acceptor needs no option: it echoes
`caps.acks: 1` when the dialer asked and the link is a stream link. The
link is an acks link when both sides list `acks >= 1` (`acks.json`
`negotiate`). Nothing changes on any other link.

### 13.2 The puback frame

On an acks link the acceptor answers every event frame with one
`puback`, in offset order, once the frame's handlers are done: every
sync handler returned and every async handler finished.

```json
{"type": "puback", "offset": "41", "id": "evt_01…", "status": "ok"}
{"type": "puback", "offset": "42", "id": "evt_01…", "status": "err", "code": "rejected", "message": "…"}
```

Pubacks are cumulative: a puback for offset N reports N and
acknowledges `ok` every earlier frame on the link that no puback of its
own reported. A sender may therefore coalesce a run of `ok` pubacks
into the last one without a wire change; an `err` must be sent on its
own. v1 senders send one per frame; receivers must already accept
coalesced ones.

| Member | Rule |
|--------|------|
| `offset` | the frame's offset, a decimal string |
| `status` | `ok` or `err` |
| `id` | the event id of the frame, when it had one; a dialer that sent another id at that offset closes the link (`puback_id`) |
| `code` | with `err`: `rejected` (a sync handler failed), `invalid` (the frame could not be used: undecodable event, reserved topic); unknown codes are kept |
| `message` | with `err`, optional, not contract |

A dialer closes the link with 1002 on a malformed puback
(`puback_malformed`) or one beyond the last frame it sent
(`puback_ahead`), and ignores one at or below its acknowledged offset.
A frame the acceptor cannot decode is still answered (`invalid`); one
whose offset it cannot read is a protocol error, since a later
cumulative puback would acknowledge it.

### 13.3 Dialer

Per outbound address the dialer keeps an acknowledged offset (the last
puback's) and the waits of acknowledged publishes by offset. The
acknowledged offset survives reconnects.

- **link up, acks:** the send cursor rewinds to the acknowledged
  offset, so frames lost with the previous link go out again, in order
  (`WithAcks` implies `WithOutbox`)
- **link up, no acks:** every wait fails `unsupported`, and so does
  every acknowledged publish sent on that link; the acknowledged offset
  follows the cursor, so a later acks link does not resend what this
  one sent
- **not sent:** an entry the writer skips (origin, subscription, link
  filter) fails `filtered`; one evicted before the writer reached it
  fails `evicted`; an event the topic filter kept out of the log fails
  `filtered`
- **disconnect:** `Disconnect(addr)` and `Close` fail its waits
  `unavailable`
- **deadline:** the publisher's context, else `WithAckTimeout`
  (default 30 s), fails what is left with `timeout`. A timeout is
  unknown, not lost: the frame may still be delivered and acknowledged
  later. Retry with the same event id.

`acks.json` `dialer` pins these rules over a scripted sequence.

### 13.4 Acceptor

The acceptor remembers, per dialer stream id, the highest offset it
received and the outcome of recent frames. A frame at or below that
offset is a copy:

- still being handled: its puback waits for the first copy's result
- handled with `err`: the stored result is sent again
- otherwise: `ok`

A copy is never published again. The memory is bounded (1 024 dialer
streams, least recently used forgotten first; failed outcomes kept for
1 024 offsets behind the highest), and lost on restart, after which
dedupe falls to the event id.

Results may settle out of order (async handlers), but pubacks go out in
offset order, which keeps them cumulative. Each acks link holds at most
`WithMaxPendingAcks` unanswered frames; at that bound its reader stops,
which backpressures the dialer through TCP.

### 13.5 Ordering, idempotency and limits

- publish order is unchanged: the event enters the log as any other
- at least once per acks link, deduplicated by `(dialer stream,
  offset)` while the acceptor runs, by event `id` across restarts
- `WithMaxPendingAcks(n)` (default 1 024) bounds unresolved
  acknowledged publishes per address; `PublishAcked` blocks for a slot
  until its context ends, and the event is then not published

### 13.6 Using it

`(*NetworkAdapter).PublishAcked(ctx, e, addrs...)` waits for local
handlers and for `addrs`, or every registered outbound address. The
bus from `New(WithNetwork(...))` with `WithAcks` and a bus from `New()`
implement `AckPublisher`; `AckPublisherOf(b)` returns nil when `b`
cannot confirm delivery to everything it forwards to. The consent
relay uses it when its publisher supports it: the ack cursor advances
only when every awaited peer acknowledged the record, and a nack or
timeout leaves the record for the next flush, under the same
idempotency key.

## Go API

```go
// options
func WithStreamLog(size int) NetworkOption
func WithLog(l StreamLog) NetworkOption
func WithHeartbeat(d time.Duration) NetworkOption
func WithMaxSubscriptions(n int) NetworkOption
func WithSubscribe(patterns ...string) NetworkOption // session.subscribe on every connect
func WithScope(s Scope) NetworkOption
func WithResumeFrom(addr string, p StreamPosition) NetworkOption
func WithOutbox() NetworkOption
func WithOutboxDropFunc(f func(addr string, n uint64)) NetworkOption
func WithAcks() NetworkOption // implies WithOutbox
func WithAckTimeout(d time.Duration) NetworkOption
func WithMaxPendingAcks(n int) NetworkOption
func WithSkipFunc(f func(addr string, n uint64)) NetworkOption
func WithSubscribeAuthorizer(f func(ctx context.Context, p Peer, s Subscription) error) NetworkOption
func WithLinkFilter(f func(p Peer, e Event) bool) NetworkOption
func WithNetworkErrorFunc(f ErrFunc) NetworkOption
func OpenAuth() Authenticator

// adapter methods
func (n *NetworkAdapter) Dial(addr string)
func (n *NetworkAdapter) Subscribe(ctx context.Context, addr string, patterns ...string) (uint64, error)
func (n *NetworkAdapter) Unsubscribe(ctx context.Context, addr string, patterns ...string) (uint64, error)
func (n *NetworkAdapter) SetScope(ctx context.Context, addr string, s Scope) (uint64, error)
func (n *NetworkAdapter) Position(addr string) (StreamPosition, bool)
func (n *NetworkAdapter) LinkState(addr string) (LinkState, bool)
func (n *NetworkAdapter) OutboxDropped(addr string) uint64
func (n *NetworkAdapter) Skipped(addr string) uint64
func (n *NetworkAdapter) PublishAcked(ctx context.Context, e Event, addrs ...string) (Ack, error)

// acknowledged publish, any bus
type AckPublisher interface{ PublishAcked(ctx context.Context, e Event) (Ack, error) }
func AckPublisherOf(b any) AckPublisher
func PublishAcked(ctx context.Context, b Bus, e Event) (Ack, error)
type Ack struct{ ID string; Offset uint64; Peers []PeerAck }
type PeerAck struct{ Peer, Code, Message string } // Code "" = acknowledged
type AckError struct{ Peer, Code, Message string } // errors.Is ErrNack; ErrAcksUnsupported for "unsupported"
```

`Subscribe`, `Unsubscribe` and `SetScope` return the ack's `at`. They
update the set that the adapter sends in `session` on every reconnect.
On a disconnected address they only update that set. On a legacy link
they return `ErrStreamsUnsupported`. Called with the context of a
handler that the same link is delivering (`link.connected`,
`stream.invalidated`, an event from that peer) they return an error
wrapping `ErrReentrantRequest`: only that delivering goroutine could
read the reply. Async handlers run off that goroutine, so the marker is
cleared for them. A request blocked on a reply also returns when its
link closes.

Handshake failures are classified: a session error and a protocol error
are final (no retry until the next explicit `Connect`); a rejected
token (code `auth`) or a missing ack (code `timeout`) is retried by a
reconnect loop; a first explicit `Connect` that fails starts none. Each
is reported through `WithNetworkErrorFunc` and published as
`link.disconnected` with its `final` and `code`, once per change.

Authorizers run on the link's writer so the ack's `at` stays an exact
fence; they must be fast.

## Tests

Tests run in `go/runtime/bus` with `-race`. They synchronize through
events and channels, never sleeps. Heartbeat ticks are injected.

| Area | Tests |
|------|-------|
| handshake | caps both ways and version minimum; plain ack to a legacy dialer (bytes); legacy acceptor gets no control frames; no auth stays legacy; `session_error` |
| format | negotiated per peer; configured format kept without a handshake |
| subscribe | pattern and scope filter; first subscription replaces `#`; ack `at` and every error code; cap; authorizer at handshake, legacy accept and subscribe; scope-only change; set re-sent on reconnect; link filter |
| order | single-publisher order; vetoed event never forwarded; adapter without the hook keeps the async forwarder and advertises `streams: 0` |
| resume | replays missed events in order; `evicted` and `ahead` (cursor rule) resets; stream restart; seeded position; first connect to legacy with a position invalidates |
| liveness | heartbeat keeps a quiet subscription from resetting; dead link detected after three silent ticks |
| link state | connected / disconnected order and payloads; `final` on session error |
| relay | per-hop offsets; upstream reset reaches subscribers that do not match its topic; origin exclusion on replay; reserved topics dropped inbound |
| outbox | flushes in order after reconnect; overflow drops oldest and counts; off keeps the drop behaviour; offline start |
| legacy skips | counter and callback |
| acks | puback per frame; veto `rejected`; async handlers hold the ack (local and remote); pubacks in offset order under out-of-order completion; copies after a reconnect deduped and answered with the stored result; lost link sent again and handled once; cumulative puback resolves earlier frames; `timeout`, `evicted`, `unavailable`, `filtered`; old peer in either direction: no puback, `unsupported`; pending cap blocks |

Re-break checks:

- heartbeat dead detection
- the filter bypass for gap markers
- first-connect invalidation
- the `ahead` cursor rule

## Vectors

[`contracts/bus-v1/streams/`](../../../contracts/bus-v1/streams/README.md). The Go
runners are `go/runtime/bus/stream_conformance_test.go` and
`stream_wire_conformance_test.go`, and they are strict: unknown
fields, a wrong `version` and unconsumed files all fail it.

| File | Pins |
|------|------|
| `offsets.json` | offset token grammar |
| `patterns.json` | subscribe pattern validity |
| `handshake.json` | auth frame and ack decoding, version minimum, ack shape (plain or extended), frame-format choice |
| `control.json` | classification on stream and legacy links; control frame parsing and error codes |
| `filter.json` | the per-link delivery predicate, gap-marker bypass |
| `resume.json` | the acceptor decision table |
| `cursor.json` | dialer cursor rules: ack plus frame sequence in; delivered, dropped, invalidated, protocol error and position out |
| `inbound.json` | bounded inbound queue: overflow closes and resumes from the last delivered position |
| `outbox.json` | ordered flush and drop-oldest counting over a scripted publish, disconnect and reconnect sequence |
| `event_frames.json` | `offset` / `kitoffset` encode on stream links, stripping on every link |
| `acks.json` | `caps.acks` and the dialer stream id in the auth frame, acks negotiation, puback parse and encode, the dialer's ack tracking (cumulative pubacks, rewind, unsupported, evicted) |
| `wire/*.jsonl` | transcripts replayed against a Go acceptor: plain ack, extended ack with replay, reset in the ack, subscribe round trip, pubacks with a copy and an invalid frame |

The pure vectors (all but `wire/`) need no sockets and no clock.
