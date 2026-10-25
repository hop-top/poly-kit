# Bus network streams

Status: proposed. Design for review; nothing here is implemented yet.

Defines stream semantics for the bus `NetworkAdapter`
([`go/runtime/bus`](../../../go/runtime/bus/README.md)): capability
advertisement, remote subscribe, resume from an offset, the reset signal,
and an offline outbox. Go is canonical. This page and the vectors under
`contracts/bus-v1/streams/` are the cross-SDK contract that other SDKs
port against.

## Context

Today the network layer is a symmetric, best-effort fan-out:

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
  A peer that predates this design publishes an unknown control frame on
  its local bus as an event with an empty topic (accepted in `ModeWarn`)
  and, as a relay, forwards it further

A networked consumer (a desktop shell talking to a core hub) needs:
subscribe by topic pattern and workspace scope; resume after reconnect
with no gaps and no duplicates; an explicit signal when resume is
impossible, so it can mark live views stale and refetch rather than show
stale data as live; and publishes made while offline kept in order.

## Goals and non-goals

Goals:

- one wire contract, additive on top of today's frames
- peers that predate it keep working, byte for byte, and never receive a
  frame they would misread
- ordered delivery per sending adapter; resume without gaps or duplicates
  within one stream; an explicit reset whenever that promise breaks
- frame format chosen per link from what the peer advertises

Non-goals for streams v1:

- durable logs: the log is in memory and dies with the process
- exactly-once delivery end to end
- acknowledged publishes from a dialer to a hub (see open decision 6)
- a relay subscribing upstream on behalf of its downstream peers
- per-tenant isolation policy: kit provides the scope filter and an
  authorization hook, the hub decides

## Terms

| Term | Meaning |
|------|---------|
| link | one WebSocket connection between two adapters |
| dialer / acceptor | the side that called `Connect` / the side serving `Handler` |
| stream | the ordered sequence of events one adapter sends; identified by a stream id minted per adapter instance |
| log | the adapter's bounded in-memory ring holding the recent part of its stream |
| offset | an event's position in its stream: `1, 2, 3, …`, assigned once at log append |
| position | `(stream id, offset)` of the last event a receiver delivered from one link |
| stream link | a link on which both sides advertised `streams >= 1` in the handshake |
| legacy link | any other link; behaves exactly as today |

## Design

### 1. One log per adapter, one cursor per link

Every adapter keeps a log: a ring of the events it forwards, in the order
it accepted them. An event enters the log when the adapter would forward
it today: a local publish, or (relay mode only) an inbound network event.
`TopicFilter` applies before the log. Each entry holds the event, its wire
origin and its offset.

Each link has a cursor into the log and one writer goroutine that sends
entries after the cursor, in offset order, applying the link's filters
(subscription, scope, origin exclusion). Replay and live delivery are the
same loop: a resumed link starts with its cursor in the past, a fresh one
at the head. Writes keep today's 5 s timeout and the 3-consecutive-failure
eviction.

Ordering needs log append to happen in publish order and only for events
the bus accepted (no sync handler vetoed it). Neither the current async
forwarder (unordered) nor a plain sync subscriber (runs before
later-registered sync handlers, so it would log vetoed events) gives both.
See open decision 1.

Default log size: 1024 entries; `WithStreamLog(n)` sets it. A hub that
serves resuming clients sizes it for its expected reconnect window.

### 2. Capability advertisement in the handshake

Both directions advertise inside the existing auth exchange. Every new
key is ignored by a peer that predates this design (`Connect` decodes the
ack's `type` only; `Handler` decodes the auth frame's `auth` only).

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

Acceptor to dialer, the ack:

```json
{
  "type": "auth_ok",
  "caps": {"streams": 1, "frames": ["kit", "cloudevents"]},
  "origin": "0f1e2d3c4b5a6978",
  "stream": {"id": "stm_01jg000000e00a000000000000", "oldest": "17", "head": "57"},
  "session": {"resume": {"status": "replaying", "after": "41", "head": "57"}}
}
```

| Key | Rule |
|-----|------|
| `caps.streams` | integer protocol version; absent or `0` means none. The link's version is the minimum of both sides; v1 is `1` |
| `caps.frames` | formats this peer decodes, from `kit`, `cloudevents`; unknown names ignored |
| `origin` | the sender's loop-prevention origin id, known before the first event (today it is learned from the first inbound frame) |
| `stream` | acceptor's stream id, oldest retained offset and head. `oldest` is `head + 1` when the log is empty; `head` is `"0"` before the first event |
| `session` | the dialer's initial subscription, scope and resume request; applied atomically before the link is registered, so no event slips between ack and replay |
| `session.resume` (ack) | outcome, see section 5. Absent when the dialer asked for no resume |

Streams need the handshake, and today the handshake exists only when both
sides configure an `Authenticator`. Without one, a link is legacy. A
deployment that wants streams but no secret uses `StaticTokenAuth` with a
fixed token. See open decision 2 for a handshake that works without auth.

The handshake is symmetric in what it advertises but not in what it
requests: only the dialer sends `session`. Stream semantics flow from
acceptor to dialer in v1 (hub to client). The dialer's own stream reaches
the acceptor through the outbox (section 7) without resume.

### 3. Frame format per link

A sender picks per link: its configured `FrameFormat` if the peer listed
it in `caps.frames`, otherwise `kit`. The default stays `FrameKit` in
every SDK. With advertisement in place, an adapter set to
`FrameCloudEvents` still talks to a legacy peer, because that peer gets
kit frames.

Receivers keep accepting both formats on every link (detection by the
exact `specversion` key, unchanged). Encoding happens once per format per
entry, not once per link.

Behaviour change: today `WithFrameFormat(FrameCloudEvents)` sends
CloudEvents frames to every peer, including peers that decode them but
predate advertisement. After this change those peers get kit frames,
which they also decode. Nothing breaks; such peers lose `subject`,
`datacontenttype`, `dataschema` and extensions on the hop, as any kit hop
does.

### 4. Remote subscribe

Stream links only. A link starts with the implicit subscription `#` (all
topics), which is today's behaviour. The first explicit subscription,
from `session.subscribe` or a `subscribe` frame, replaces the implicit
one. `session.subscribe: []` means subscribed to nothing yet.

Subscriptions are a set of patterns per link (subscribing twice to the
same pattern is a no-op), capped at 1000 patterns
(`WithMaxSubscriptions`). A pattern is valid when it is non-empty, each
`.`-separated segment is non-empty, `*` and `#` appear only as whole
segments, and `#` only as the last segment. Matching is `Topic.Match`.

Scope is per link, not per pattern: `{"workspaces": [...]}`. An event
passes when its `workspace_id` is in the list, or it has no workspace id
(global events always pass; see open decision 4). No scope means all
workspaces. A `subscribe` frame that carries `scope` replaces the link's
scope.

A hub can authorize subscriptions with
`WithSubscribeAuthorizer(func(ctx, Peer, Subscription) error)`. `Peer`
carries the remote address, the advertised origin and the auth token the
link presented. A non-nil error refuses the request with code
`forbidden`.

Subscription changes take effect from the link's cursor forward; adding
a pattern never replays history.

### 5. Offsets, resume and reset

Offsets:

- assigned by the sending adapter at log append; never by the publisher
  and never carried over from an upstream hop
- unsigned 64-bit, starting at 1, dense (+1 per entry) within a stream
- per stream, not per topic: a receiver sees gaps where its filters drop
  entries, so density cannot be used to detect loss; resume is what
  guarantees no loss
- a stream id is a TypeID with prefix `stm`, minted when the adapter
  starts. A restarted adapter has a new stream, so old offsets can never
  be mistaken for new ones
- on the wire, a decimal string with no sign and no leading zeros
  (`"0"`, `"42"`, `"18446744073709551615"`). JSON numbers lose precision
  above 2^53, and CloudEvents integers stop at 2^31-1. Receivers treat the
  token as ordered: they may compare two offsets of the same stream as
  unsigned integers, and nothing else

Kit event frames on a stream link gain `offset`; CloudEvents frames gain
the extension `kitoffset` (a string). Both are stripped on decode and
never reach subscribers. A handler reads the position from its context:
`bus.StreamPositionFrom(ctx) (StreamPosition, bool)`, where
`StreamPosition{Peer, Stream string; Offset uint64}`. A consumer that
carries offsets in its own CloudEvents extension maps it from there. See
open decision 3 for the alternative of a field on `Event`.

Resume request: `session.resume = {stream, after}` asks for every entry
with offset greater than `after`. `after: "0"` asks for the whole stream.
No `resume` means start live at the head.

The acceptor decides, given its log `(S, oldest O, head H)` and the
request `(s, a)`, in this order:

| Condition | Outcome | Cursor |
|-----------|---------|--------|
| no `resume` | none (key absent) | `H` |
| `s != S` | `reset`, reason `stream` | `H` |
| `a > H` | `reset`, reason `ahead` | `H` |
| `a < O - 1` | `reset`, reason `evicted` | `H` |
| otherwise | `replaying` (`a == H` replays nothing) | `a` |

A reset outcome in the ack:

```json
{"type": "auth_ok", "caps": {"streams": 1, "frames": ["kit", "cloudevents"]}, "origin": "0f1e2d3c4b5a6978", "stream": {"id": "stm_01jg000000e00a000000000000", "oldest": "17", "head": "57"}, "session": {"resume": {"status": "reset", "reason": "evicted", "after": "5", "head": "57"}}}
```

Mid-stream reset: if a link's cursor falls behind the oldest entry (a
slow or stalled receiver), the writer sends a `reset` control frame with
reason `lagged` and jumps the cursor to the head. The link stays up.

```json
{"type": "reset", "stream": "stm_01jg000000e00a000000000000", "reason": "lagged", "after": "120", "head": "1300"}
```

Dialer side, on any reset (ack or frame), the adapter publishes one
local event before delivering anything after it, so it is ordered against
the stream:

- topic `kit.runtime.stream.invalidated` (open decision 5)
- source `kit.bus.network`
- payload `{"peer", "stream", "reason", "after", "head"}`, offsets as
  decimal strings
- published with the network-origin context marker, so a non-relay
  adapter never forwards it; a relay forwards it like any inbound event
  (section 8)

The dialer also publishes it, with reason `unsupported`, when it
reconnects (not on first connect) to a peer that did not advertise
streams. A reconnect to such a peer can lose events silently, and
consumers must not treat their views as live.

Consumer contract: on `kit.runtime.stream.invalidated`, mark every live
view fed by that peer stale and refetch it; events that follow are live.

Position tracking, dialer side:

- the adapter records the position per outbound address after each
  delivered frame, whether or not the local `Publish` returned an error
  (a veto is a local decision, not a delivery failure)
- frames whose offset is not greater than the recorded one are dropped
  as duplicates
- on reconnect the adapter sends the recorded position as
  `session.resume` automatically
- `WithResumeFrom(addr, StreamPosition)` seeds the position, for a
  process that persisted it; `(*NetworkAdapter).Position(addr)` reads it

Filtered-out entries do not move the receiver's position, which can then
fall behind the acceptor's oldest entry while nothing relevant was lost,
causing a spurious `evicted` reset. The acceptor therefore sends a
`position` control frame when its writer is caught up and the cursor has
passed entries the link did not receive, at most once per second:

```json
{"type": "position", "offset": "1450"}
```

The dialer records it like an event offset (open decision 7).

### 6. Control frames

Stream links only, never sent to a legacy peer. A control frame is a JSON
object with no `specversion` member and with a string member `type`. Kit
event frames never carry `type`; CloudEvents frames are recognised first
by `specversion`. On a legacy link classification stays as it is today.
Control frames have the same shape under either frame format; they are
never CloudEvents.

| `type` | Direction | Members | Reply |
|--------|-----------|---------|-------|
| `subscribe` | either | `id`, `patterns` (non-empty array), `scope`? | `ack` or `error` |
| `unsubscribe` | either | `id`, `patterns` | `ack` (removing an absent pattern is not an error) |
| `ack` | reply | `id` | none |
| `error` | reply | `id`?, `code`, `message` | none |
| `reset` | acceptor to dialer | `stream`, `reason`, `after`, `head` | none |
| `position` | acceptor to dialer | `offset` | none |

```json
{"type": "subscribe", "id": "s2", "patterns": ["acme.billing.invoice.*"], "scope": {"workspaces": ["ws1", "ws2"]}}
{"type": "ack", "id": "s2"}
{"type": "unsubscribe", "id": "u1", "patterns": ["acme.crm.#"]}
{"type": "error", "id": "s3", "code": "invalid_pattern", "message": "segment 2: '#' must be the last segment"}
{"type": "error", "code": "unknown_type", "message": "unknown control frame type \"resume\""}
```

`id` is chosen by the sender: a non-empty string up to 64 bytes, echoed
in the reply. Requests are processed in order per link. Error codes are
contract; messages are not.

| Code | When |
|------|------|
| `bad_request` | missing or ill-typed member, empty `id`, empty `patterns` |
| `invalid_pattern` | a pattern fails the rule in section 4 |
| `invalid_scope` | `scope` is not an object of string arrays |
| `limit` | the request would exceed the subscription cap; nothing from it applies |
| `forbidden` | the subscribe authorizer refused it |
| `unknown_type` | `type` is not in the table; no `id` echoed unless present |

A request is all or nothing. Handshake `session` errors use the same
codes: the acceptor sends `auth_ok` with `"session": {"error": {"code",
"message"}}` and closes with 1008 (`invalid session`). `Connect` returns
`*StreamError{Code, Message}`, and the reconnect loop treats it as
permanent and stops.

### 7. Offline outbox (dialer)

Opt-in: `WithOutbox()`. The bus stays "not a queue" by default.

The outbox is the dialer's own log seen through the cursor of a
disconnected outbound peer. The cursor survives the disconnect, and on
reconnect the writer resumes from it, so queued events go out in publish
order before any later event. No separate queue exists.

- bound: the log size. When the ring evicts entries a disconnected
  cursor has not reached, they are dropped oldest first
- counter: `(*NetworkAdapter).OutboxDropped(addr) uint64`, cumulative per
  address, plus an optional `WithOutboxDropFunc(func(addr string, n
  uint64))` called once per eviction batch
- scope: outbound addresses only, from the first successful `Connect`
  until `Disconnect(addr)` or `Close`. Inbound peers have no outbox. Events
  published before the first connect are not queued
- works on legacy links too, because it is purely sender-side
- with the outbox off, a reconnect starts the cursor at the head, which
  matches today's drop-while-disconnected behaviour

### 8. Relay

A relay logs inbound events into its own stream and assigns its own
offsets; the upstream offset is stripped on decode and never forwarded.
Each hop's offsets belong to the adapter that sent the frame. A client of
relay B resumes against B's stream; B, as a dialer of hub A, resumes
against A's.

The origin exclusion moves into the per-link filter. A relay knows each
peer's origin from the handshake (or learns it from the first frame on a
legacy link, as today) and skips entries whose wire origin equals it,
on replay as well as live.

A reset that B receives from A becomes a local
`kit.runtime.stream.invalidated` on B, which B relays like any inbound
event. B's clients receive it in order, with B's offset, and learn that
their view of A's events has a hole.

### 9. Delivery guarantees

| Path | Guarantee |
|------|-----------|
| one link, while up | each entry sent at most once, in offset order |
| acceptor to dialer, across a reconnect, same process on both sides | no gaps and no duplicates for the subscription, or an explicit reset |
| acceptor to dialer, dialer restarted with a persisted position | at least once: entries after the persisted position are replayed; dedupe by `(stream, offset)` or event `id` |
| acceptor restarted | no replay: new stream id, reset with reason `stream` |
| dialer to acceptor, outbox on | queued events once, in order; frames in flight when the link dropped are lost (at most once); overflow drops oldest, counted |
| dialer to acceptor, outbox off | today's behaviour |
| end to end | no exactly-once; a sync handler veto does not hold back the position |

Ordering is per sending adapter. Two publishers in different processes
have no relative order beyond the order their events enter a common
relay's log.

### 10. Backward compatibility

| Pair | Result |
|------|--------|
| new dialer, legacy acceptor | ack has no `caps`, so the link is legacy: kit frames, no offsets, no control frames. Outbox still works. A reconnect publishes `kit.runtime.stream.invalidated` (reason `unsupported`) |
| legacy dialer, new acceptor | auth frame has no `caps`, so the link is legacy: implicit `#`, kit frames, no offsets, today's frame bytes. The ack gains keys the legacy dialer ignores |
| new peers, no `Authenticator` | legacy link, exactly as today |
| new peers, auth, nothing configured | stream link with implicit `#`: same events as today, plus `offset` on each frame |
| frames vectors | unchanged: kit-frame `offset` and control-frame classification apply only on stream links |

Behaviour changes visible to existing adopters, all on paths they already
use:

- forwarding becomes ordered per sender, through per-link writers instead
  of one goroutine per event
- a slow legacy link skips evicted entries instead of stalling the
  forwarder; the 3-write-failure eviction stays
- `FrameCloudEvents` falls back to kit frames for peers that do not
  advertise CloudEvents (section 3)
- `kitoffset` is reserved like `kitorigin` (open decision 8)

## Go API sketch

```go
// options
func WithStreamLog(size int) NetworkOption
func WithMaxSubscriptions(n int) NetworkOption
func WithSubscribe(patterns ...string) NetworkOption // session.subscribe on every connect
func WithScope(s Scope) NetworkOption
func WithResumeFrom(addr string, p StreamPosition) NetworkOption
func WithOutbox() NetworkOption
func WithOutboxDropFunc(f func(addr string, n uint64)) NetworkOption
func WithSubscribeAuthorizer(f func(ctx context.Context, p Peer, s Subscription) error) NetworkOption

// adapter methods
func (n *NetworkAdapter) Subscribe(ctx context.Context, addr string, patterns ...string) error
func (n *NetworkAdapter) Unsubscribe(ctx context.Context, addr string, patterns ...string) error
func (n *NetworkAdapter) SetScope(ctx context.Context, addr string, s Scope) error
func (n *NetworkAdapter) Position(addr string) (StreamPosition, bool)
func (n *NetworkAdapter) OutboxDropped(addr string) uint64

// types and helpers
type Scope struct{ Workspaces []string }
type Subscription struct{ Patterns []string; Scope *Scope }
type Peer struct{ Addr, Origin, Token string }
type StreamPosition struct{ Peer, Stream string; Offset uint64 }
type StreamError struct{ Code, Message string } // errors.Is(err, ErrStream)
func StreamPositionFrom(ctx context.Context) (StreamPosition, bool)
const TopicStreamInvalidated = "kit.runtime.stream.invalidated"
var ErrStreamsUnsupported error // Subscribe on a legacy link
```

`Subscribe`, `Unsubscribe` and `SetScope` wait for the reply and update
the set the adapter re-sends in `session` on every reconnect. On a
disconnected address they only update that set.

Files: `network_stream.go` (handshake, caps, control frames),
`network_subscribe.go`, `network_resume.go` (log, cursor, resume
decision), `network_outbox.go`, plus `stream_conformance_test.go`.

## Test plan

Unit and integration tests in `go/runtime/bus`, all run with `-race`:

| Test | Pins |
|------|------|
| `TestHandshake_AdvertisesCaps` | both directions carry `caps` and `origin`; link version is the minimum |
| `TestHandshake_LegacyAcceptorGetsNoControlFrames` | old-shape ack: no control frames, no `offset`, frame bytes as today |
| `TestHandshake_LegacyDialerSeesTodaysFrames` | old-shape auth frame: implicit `#`, frames without `offset` |
| `TestHandshake_NoAuthIsLegacy` | without an `Authenticator` nothing new is sent |
| `TestFrameFormat_NegotiatedPerPeer` | CE to an advertising peer, kit to a legacy one, from one adapter |
| `TestSubscribe_PatternAndScopeFilter` | patterns and workspace scope gate delivery; global events pass |
| `TestSubscribe_ReplacesImplicitAll` | first subscription replaces `#` |
| `TestSubscribe_AckAndErrorCodes` | every code in section 6; all-or-nothing |
| `TestSubscribe_MaxSubscriptions` | cap 1000, `limit` |
| `TestSubscribe_Authorizer` | `forbidden`; handshake session error closes 1008 and stops reconnect |
| `TestSubscribe_ResentOnReconnect` | set changes survive a reconnect |
| `TestStream_SinglePublisherOrder` | many sequential publishes arrive in order |
| `TestStream_VetoedEventNotForwarded` | a sync veto keeps the event out of the log |
| `TestResume_ReplaysMissedInOrder` | kill the link, publish, reconnect: replay in order, then live, no duplicates |
| `TestResume_OutOfRangeSendsReset` | `evicted` and `ahead` outcomes, invalidation event before later events |
| `TestResume_StreamRestartSendsReset` | new acceptor instance, reason `stream` |
| `TestResume_SeededPosition` | `WithResumeFrom` |
| `TestResume_PositionFrameAvoidsSpuriousReset` | filtered-out traffic does not cause `evicted` |
| `TestStream_LaggedLinkGetsReset` | stalled reader: in-band `reset` `lagged`, link stays up |
| `TestStream_PositionInContext` | `StreamPositionFrom` in sync and async handlers |
| `TestStream_ReconnectToLegacyPublishesUnsupported` | reason `unsupported` on reconnect only |
| `TestOutbox_FlushesInOrderAfterReconnect` | events published while down arrive first, in order |
| `TestOutbox_OverflowDropsOldestAndCounts` | ring eviction, counter and callback |
| `TestOutbox_OffKeepsDropBehaviour` | default unchanged |
| `TestOutbox_LegacyAcceptor` | outbox flushes to a legacy peer |
| `TestRelay_PreservesOffsetsPerHub` | client of B sees B's dense offsets; upstream offsets never forwarded |
| `TestRelay_ForwardsUpstreamReset` | A's reset reaches B's clients as an ordered event |
| `TestRelay_OriginExclusionOnReplay` | a peer never gets its own events back on replay |

Existing network tests stay green unchanged, except where a test pins the
unordered forwarding or the CloudEvents-to-everyone behaviour, which the
review will list.

## Vectors

`contracts/bus-v1/streams/`, with a `README.md` in the bus-v1 style
(`version: "v1"`, a `description` per file, strict runners, Go decides
expectations). Go runner: `go/runtime/bus/stream_conformance_test.go`.

| File | Cases | Pins |
|------|-------|------|
| `handshake.json` | `auth[]`, `ack[]` | decoding both handshake frames: caps, version minimum, frame list, session; old shapes decode as legacy |
| `frame_choice.json` | `cases[]` `{configured, peer_frames, send}` | per-link format selection |
| `offsets.json` | `cases[]` `{token, ok, value}` | offset grammar: decimal, no leading zeros, no sign, at most 2^64-1, JSON string only |
| `control.json` | `decode[]`, `encode[]` | classification (CE, control, kit) on stream and legacy links; every control type; error codes for malformed requests |
| `patterns.json` | `cases[]` `{pattern, ok, code}` | subscribe pattern validity |
| `filter.json` | `cases[]` `{subscriptions, scope, peer_origin, event, origin, deliver}` | link filter: patterns, scope, global events, origin exclusion |
| `resume.json` | `cases[]` `{log, request, outcome, replay}` | the resume decision table and the replayed offsets |
| `outbox.json` | `cases[]` `{capacity, ops, sent, dropped}` | ordered flush and drop-oldest counting from a scripted publish, disconnect and reconnect sequence |
| `event_frames.json` | `decode[]`, `encode[]` | kit `offset` and CE `kitoffset` on stream links, stripping, ignored on legacy links |
| `wire/*.jsonl` | transcripts | handshake, resume replay, reset in the ack, lagged reset, subscribe round trip, legacy pairs; replayed against an adapter by both runners |

The pure vectors (all but `wire/`) need no sockets, so a port can pass
them before it has a network stack.

## Open decisions

1. Ordered, veto-aware log append. Options: (a) a sync subscriber, which
   logs events vetoed by sync handlers registered after it; (b) a small
   bus-core hook: an observer called after all sync handlers succeed, in
   the publishing goroutine (`bus.go`, plus an optional interface so other
   `Bus` implementations fall back to (a)). Recommended: (b).
2. Handshake without auth. Options: (a) streams require an
   `Authenticator`, use `StaticTokenAuth` for open setups; (b) a hello
   sent as a JSON array, which every existing peer drops, with an ack
   timeout to detect legacy acceptors. Recommended: (a); (b) can follow as
   streams v2 if needed.
3. Where receivers read the offset. Options: (a) context accessor
   `StreamPositionFrom`; (b) an `Offset` field on `Event`. Recommended:
   (a); `Event` stays a pure envelope, and position is link state.
4. Global events under a scope. Options: always delivered, or only with
   `"global": true` in the scope. Recommended: always delivered; a hub
   that must hide them filters with the authorizer or `TopicFilter`.
5. Reset topic name. `kit.runtime.stream.reset` fails `ValidateTopic`'s
   past-tense rule. Options: `kit.runtime.stream.invalidated`, or add
   `reset` to the past-tense whitelist. Recommended: `invalidated`.
6. Acknowledged dialer publishes. The acceptor could return, in the ack,
   the last offset it received from the dialer's stream (keyed by the
   dialer's stable origin), making the outbox at-least-once with no loss
   in flight. Recommended: defer to streams v2.
7. `position` frames. Recommended: yes, in v1; without them a quiet
   subscription on a busy hub resets on every reconnect.
8. Reserving `kitoffset`. Rejecting it in `Event.Extensions`, as
   `kitorigin` is, takes a one-line hook in the CloudEvents rules.
   Recommended: reserve it.
9. Outbox default. Recommended: off (opt-in), so adopters relying on
   drop-while-disconnected see no change.
10. Session error on reconnect. Recommended: permanent, stop the loop and
    report through the adapter's error func, rather than retry forever on
    a configuration error.
