# bus-v1

## What it answers

What `kit/runtime/bus` must accept, reject and emit, independent of language: topic matching, the two topic validators, the topic builder and `ParseTopic` with their exact error texts, `PrefixTopics`, qualifiers, enforcement-mode parsing and the event envelope JSON and its ids. Wrong place for transport behaviour (network adapter, sinks, retry); those have no vectors yet.

## Use it when

- you port or change topic, qualifier, mode or envelope code in any SDK: run both runners
- you add bus behaviour to Go: add a vector first, then make every port pass it

## Quick start

```sh
make test-parity-bus
# or separately:
go test ./go/runtime/bus/ -run Conformance
cargo test --test bus_conformance --all-features   # from sdk/experimental/rs
```

## Contract

Go is canonical. The vectors are the source of truth: a runner failure means the implementation drifted, not that the vector should be edited. Every file carries `version: "v1"` and a `description` stating the rule it pins.

| File | Cases | Pins |
|------|-------|------|
| `match.json` | `cases[]` `{pattern, topic, match}` | `*` one segment, trailing-only `#`, exact and case-sensitive; split on `.` so `""` is one empty segment |
| `validate.json` | `cases[]` `{topic, ok, error | error_contains}` | published-topic validator: 4 segments, `^[a-z][a-z0-9_]*$`, <= 128 bytes, no wildcards, no tense rule; `error` is the exact full message including the `bus: invalid topic: "<topic>" (<reason>)` wrapper |
| `validate_topic.json` | `cases[]` `{topic, ok, error | error_contains}` | construction-time validator: leading digit or underscore allowed, no length cap, past-tense action (`ed` suffix or whitelist, all 28 entries covered); 0-based positions; exact `error`, except the not-past-tense cases, which use `error_contains` (the reason before the trailing "see ..." pointer) |
| `builder.json` | `cases[]` `{source, category, object, ctor_modifiers, mods, action, ok, topic | error | error_contains}` | `TopicOf`/`PrefixedTopicOf` + `Mod` + `Action`: modifier joins the Object segment with `_` (last wins, empty clears), the render is validated by the construction-time validator; `error` is the exact panic text, `bus.TopicOf: ` plus the invalid-topic message |
| `parse_topic.json` | `cases[]` `{topic, ok, source, category, object, modifier, action, rerender | rerender_error, error | error_contains}` | `ParseTopic`: construction-time validation, then the Object segment splits on its FIRST underscore; `rerender` is what the returned builder renders again |
| `prefix_topics.json` | `cases[]` `{prefix, actions, topics, ok, error | error_contains}` | 3-segment prefix plus actions; `topics` is the expected map, which is partial when a later action fails; exact `error` for prefix errors, `error_contains` (quoted prefix argument plus the inner validator message) for action errors |
| `qualifiers.json` | `serialize[]`, `extract[]` | lowercase omitempty keys, `IsZero`; which payload shapes yield qualifiers |
| `mode_parse.json` | `display{}`, `cases[]` `{input, mode, ok, error}` | `ModeFromString`: trim, case-insensitive, empty is warn; failure returns warn plus an error quoting the untrimmed input |
| `event_json.json` | `cases[]`, `decode_reject[]` | envelope members in Go's order: `topic`, `source`, `timestamp`, `payload`, then `workspace_id`, `id`, `subject`, `datacontenttype`, `dataschema`, `extensions` (each omitted when empty, extensions in ascending name order); unknown wire keys ignored; null and absent members unset; wrongly typed members and malformed timestamps rejected; `text` pins exact bytes |
| `event_timestamp.json` | `cases[]` `{timestamp, canonical}`, `reject[]`, `defaults[]` | `timestamp` decode: the accepted grammar, the form Go re-encodes it as, what is refused, and the zero time for an absent or null timestamp |
| `event_extensions.json` | `cases[]` `{extensions, workspace_id, error}` | `Event.extensions` rules applied when an event is shaped as a CloudEvent: name `[a-z0-9]+`, reserved attribute names, `workspaceid` and `kitorigin`, string/bool/int32 values, ascending-name first failure; `error` is the exact text |
| `event_id.json` | prefix, alphabet, UUID version | shape of a minted id: `evt_` plus a 26-character TypeID suffix over a UUIDv7; ids are random so no value is pinned |

Conventions:

- Expected values are literals. Error text is compared exactly (`error`) by default; only the cases listed in the error-text section below use `error_contains`.
- Runners are strict: unknown fields, a `version` other than `v1`, an unknown event `mode`, a failing case with neither `error` nor `error_contains`, and a corpus file no runner consumes all fail the run.
- JSON documents are compared as values, not text, so key order and payload escaping do not matter.
- Timestamps in `event_json.json` are fixed strings in the canonical form Go re-encodes unchanged (fractional seconds with trailing zeros trimmed, original zone offset). No runner reads the clock.
- Error-text quoting follows Go `%q` (`strconv.Quote`): control characters, `\`, `"` and non-printable runes are escaped the Go way, and the `ctrl:` cases pin that. A port's quote helper must match, not use its language's debug formatting.
- To extend a vector file: add the case, run the Go runner, and take the expectation from what Go produces. Only Go decides expectations; never edit one to make another port pass.

## What the error text contract covers

The contract is the language-neutral core of an error, not its exact bytes. Source paths (`go/runtime/bus/topics.go`), symbol names (`pastTenseWhitelist`, `bus::PAST_TENSE_WHITELIST`), wrapper function names (`PrefixTopics`, `prefix_topics`) and list formatting of the action set are not contract.

- A case with `error` pins the whole text; both runners compare it exactly. This is the default.
- `error_contains` is used only for the not-past-tense cases in `validate_topic.json`, `builder.json` and `parse_topic.json` and the action-error cases in `prefix_topics.json`. It lists substrings the text must contain: for `validate_topic`, the reason up to the trailing "see ..." pointer; for `prefix_topics`, the quoted prefix argument plus the inner validator message.

Not in the corpus, because the languages cannot agree by construction: an embedded `Qualifiers` whose fields are all empty (Go reports found, Rust cannot tell it from none) and a plain map payload that happens to use the qualifier keys (Go: not a struct, Rust: found).

## Envelope notes

- **Timestamp precision.** Go keeps nanoseconds and writes a fraction only when it is non-zero, trailing zeros trimmed. A decoder accepts `.` or `,` and any number of digits (beyond nine are truncated), and writes the offset it read, except that a zero offset (`Z`, `+00:00`, `-00:00`) is written `Z`. Ports that hold the timestamp as text normalise on decode to that form, so the same JSON re-encodes to the same text in every SDK.
- **Offsets.** The date-time grammar is RFC 3339 with uppercase `T`/`Z`, a 4-digit year, existing calendar days, seconds 00-59 (no leap second) and an offset of hh:mm. Go's decoder additionally accepts offsets of `+24:00` and offset minutes of 60, some of which it cannot re-encode; a port may reject them, and no vector pins them. The year is not range-checked against the UTC instant for events (that check belongs to CloudEvents shaping).
- **Ids.** An id is kept verbatim on decode; only minting produces the `evt_` TypeID form. Rust's clock-seam constructor mints nothing (no randomness on `wasm32-unknown-unknown`); the caller supplies an id from the mint seam.
- **Extension integers.** Go sees an integer literal beyond 64 bits as `integer out of range`; a port that decodes numbers into doubles cannot tell it from a float, so no vector goes beyond `2^64`.

## Runners

| Port | Runner |
|------|--------|
| Go | `go/runtime/bus/conformance_test.go` (`TestConformance*`, including `TestConformanceEvent*`) |
| Rust | `sdk/experimental/rs/tests/bus_conformance.rs` (feature `bus`) |

A runner skips a section only for a capability the port lacks. Rust names each one in its `PENDING` constant (file, section), prints a skip line, and fails once the section would pass, so landing the capability forces the entry to be removed and the section asserted. Rust has none pending now.

## See also

- [`go/runtime/bus/`](../../go/runtime/bus/)
- [`sdk/experimental/rs/src/bus/`](../../sdk/experimental/rs/src/bus/)
