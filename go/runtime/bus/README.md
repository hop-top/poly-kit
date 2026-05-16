# bus

Event-driven message distribution and pub/sub for kit.

In-process publish/subscribe with MQTT-style wildcard patterns
(`*` matches one segment, `#` matches zero or more trailing
segments). Optional adapters fan events out to SQLite or to
peer processes.

## Topic grammar

Every published topic is **exactly four** dot-separated past-tense
segments:

```
[Source].[Category].[Object].[Action]
e.g.   kit.runtime.entity.created
       kit.ai.response.received
       myapp.core.breaker.tripped
```

Rules (enforced by `Validate` and `ValidateTopic`):

- exactly 4 segments separated by `.`
- each segment matches `^[a-z][a-z0-9_]*$` (lower snake_case)
- total topic length ≤ 128 characters
- the trailing Action segment is past tense (ends in `ed` or is
  whitelisted — see `pastTenseWhitelist`)
- wildcards (`*`, `#`) are NOT allowed in publish topics; they
  remain valid in subscribe patterns

### Optional Object modifier

The Object segment may carry a snake_case modifier joined with an
underscore. The wire form stays a single segment:

```
kit.config.snapshot_reload.failed
                ^^^^^^^^^^
                object   = snapshot
                modifier = reload
```

Use the modifier when the same Object participates in distinct
event flavours that should remain distinguishable on the wire
(`snapshot` vs `snapshot_reload`). Multi-word modifiers are fine —
parsing splits on the **first** underscore, so
`snapshot_partial_reload` parses as object=`snapshot`,
modifier=`partial_reload`.

See ADR-0017 for the full grammar rationale and the design pivot
from sigils to payload-side qualifiers.

## Builder API

`bus.TopicOf` is the typed-construction path for new emitters.
Action is the terminal method — it validates and returns the final
`bus.Topic`. Bad input panics at the construction site:

```go
import "hop.top/kit/go/runtime/bus"

t := bus.TopicOf("kit", "config", "snapshot").Action("reloaded")
// → "kit.config.snapshot.reloaded"

t := bus.TopicOf("kit", "config", "snapshot").
    Mod("reload").
    Action("failed")
// → "kit.config.snapshot_reload.failed"
```

`bus.PrefixedTopicOf` is a convenience for fixed prefixes with an
optional inline modifier:

```go
bus.PrefixedTopicOf("kit", "config", "snapshot", "reload").Action("failed")
// → "kit.config.snapshot_reload.failed"
```

`bus.PrefixTopics` (the existing helper) keeps working for emitters
that expand a 3-segment prefix into a `TopicMap` of past-tense
actions. The two paths coexist; pick the one that fits your shape.

## Parsing topics

`bus.ParseTopic` is the inverse of the builder. It validates the
input, splits the Object segment on the first underscore, and
returns a builder plus the action so callers can re-render or
retarget:

```go
b, action, err := bus.ParseTopic("kit.config.snapshot_reload.failed")
// b.SourceSeg()   == "kit"
// b.CategorySeg() == "config"
// b.ObjectSeg()   == "snapshot"
// b.ModifierSeg() == "reload"
// action          == "failed"

// Retarget by mutating the builder and re-rendering:
again := b.Mod("retry").Action(action)
// → "kit.config.snapshot_retry.failed"
```

Invalid input returns a `*bus.InvalidTopicError` (use `errors.As`
to extract the offending topic and the reason).

## Qualifiers convention

The four semantic axes that describe why/how/with-what/during-what
do **not** live in the topic string — they live in the payload via
`bus.Qualifiers`:

```go
type Qualifiers struct {
    Reason       string `json:"reason,omitempty"`       // why
    Mechanism    string `json:"mechanism,omitempty"`    // how
    Property     string `json:"property,omitempty"`     // with-attribute
    Circumstance string `json:"circumstance,omitempty"` // during-context
}
```

Embed `bus.Qualifiers` in any payload struct that wants qualifier
semantics. Both anonymous and named embeds work:

```go
// Anonymous embed (preferred):
type SnapshotReloadFailed struct {
    bus.Qualifiers
    SnapshotID string `json:"snapshot_id"`
}

// Named embed:
type SnapshotReloadFailed struct {
    Q          bus.Qualifiers `json:"qualifiers"`
    SnapshotID string         `json:"snapshot_id"`
}
```

The bus `Publish` API does not change. Subscribers extract
qualifiers generically via `bus.QualifiersFrom`:

```go
b.Subscribe("kit.config.snapshot_reload.failed", func(ctx context.Context, e bus.Event) error {
    if q, ok := bus.QualifiersFrom(e.Payload); ok {
        // q.Reason, q.Mechanism, q.Property, q.Circumstance
    }
    return nil
})
```

`Qualifiers` fields are opaque strings; adopters define the
controlled vocabulary per event type. An empty `Qualifiers`
JSON-marshals to `{}` because every field carries `omitempty`.

## Migration: existing emitters

For most emitters the migration is **none required**. Existing
hand-written topic constants stay valid as long as they pass
`Validate`. The new surface is purely additive.

For new code, prefer the builder over hand-written strings:

```diff
-const TopicSnapshotReloaded bus.Topic = "kit.config.snapshot.reloaded"
+var TopicSnapshotReloaded = bus.TopicOf("kit", "config", "snapshot").Action("reloaded")
```

If your event currently encodes a reason / mechanism / property /
circumstance in the topic itself (e.g. via a sigil-like character
or extra dot segments), migrate the qualifier into the payload:

```diff
-bus.NewEvent("kit.config.snapshot.reloaded?reason=sighup", "config", payload)
+payload := SnapshotReloaded{
+    Qualifiers: bus.Qualifiers{Reason: "sighup", Mechanism: "signal"},
+    // ... other payload fields
+}
+bus.NewEvent(
+    bus.TopicOf("kit", "config", "snapshot").Action("reloaded"),
+    "config",
+    payload,
+)
```

Audit checklist for adopters on upgrade:

1. Grep your topic constants for the sigil characters `?`, `+`,
   `=`, `@` and for topics with more than 4 dot segments — none
   are expected, but verify.
2. Replace string-concatenated topic constants with
   `bus.TopicOf(...).Action(...)` (or `Mod(...).Action(...)`).
3. For events that distinguish via reason / mechanism / property /
   circumstance, embed `bus.Qualifiers` in the payload struct and
   stop encoding the qualifier in the topic.

## Enforcement modes

The bus runs `Validate` on every Publish; behaviour depends on the
configured `Mode`:

- `ModeOff` — validation skipped entirely.
- `ModeWarn` — invalid topics reported via the configured
  reporter; Publish proceeds.
- `ModeStrict` — invalid topics reported AND Publish returns
  `*InvalidTopicError`; the event is not delivered.

Default is `ModeWarn`. Override precedence:
explicit `WithEnforce` > `kit.bus.enforce` config > `KIT_BUS_ENFORCE`
env > default.

```go
b := bus.New(bus.WithEnforce(bus.ModeStrict))
b := bus.New(bus.WithEnforceFromEnv())            // KIT_BUS_ENFORCE=strict
b := bus.New(bus.WithInvalidTopicReporter(report))
```

## Adopter rebrand example

```go
import "hop.top/kit/go/runtime/bus"

tm, err := bus.PrefixTopics("myapp.runtime.user",
    []string{"created", "updated", "deleted"})
// tm["created"] == "myapp.runtime.user.created"
```

Most adopters do not call `PrefixTopics` directly — they pass a
prefix to a package-level `WithTopicPrefix` option.

## See also

- [`go/runtime/notify`](../notify/README.md) — outbound notification sinks (webhook / email / OS-native) built on `bus.Sink`
