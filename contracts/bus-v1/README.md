# bus-v1

## What it answers

What `kit/runtime/bus` must accept, reject and emit, independent of language: topic matching, the two topic validators, the topic builder and `ParseTopic` with their exact error texts, `PrefixTopics`, qualifiers, enforcement-mode parsing and the event envelope JSON. Wrong place for transport behaviour (network adapter, sinks, retry); those have no vectors yet.

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
| `event_json.json` | `cases[]`, `decode_reject[]` | envelope keys `topic`, `source`, `timestamp`, `payload`, `workspace_id` (omitted when empty); RFC 3339 timestamps with offset and nanoseconds; unknown wire keys ignored; malformed timestamps rejected |

Conventions:

- Expected values are literals. Error text is compared exactly (`error`) by default; only the cases listed in the error-text section below use `error_contains`.
- Runners are strict: unknown fields, a `version` other than `v1`, an unknown event `mode`, a failing case with neither `error` nor `error_contains`, and a corpus file no runner consumes all fail the run.
- JSON documents are compared as values, not text, so key order and payload escaping do not matter.
- Timestamps are fixed strings in the canonical form Go re-encodes unchanged (fractional seconds with trailing zeros trimmed, original zone offset). No runner reads the clock.
- Error-text quoting follows Go `%q` for printable text. Vectors avoid non-printable characters other than `\n`, whose escaping the languages share.

## What the error text contract covers

The contract is the language-neutral core of an error, not its exact bytes. Source paths (`go/runtime/bus/topics.go`), symbol names (`pastTenseWhitelist`, `bus::PAST_TENSE_WHITELIST`), wrapper function names (`PrefixTopics`, `prefix_topics`) and list formatting of the action set are not contract.

- A case with `error` pins the whole text; both runners compare it exactly. This is the default.
- `error_contains` is used only for the not-past-tense cases in `validate_topic.json`, `builder.json` and `parse_topic.json` and the action-error cases in `prefix_topics.json`. It lists substrings the text must contain: for `validate_topic`, the reason up to the trailing "see ..." pointer; for `prefix_topics`, the quoted prefix argument plus the inner validator message.

Not in the corpus, because the languages cannot agree by construction: an embedded `Qualifiers` whose fields are all empty (Go reports found, Rust cannot tell it from none) and a plain map payload that happens to use the qualifier keys (Go: not a struct, Rust: found).

## Runners

| Port | Runner |
|------|--------|
| Go | `go/runtime/bus/conformance_test.go` (`TestConformance*`) |
| Rust | `sdk/experimental/rs/tests/bus_conformance.rs` (feature `bus`) |

A runner skips a section only for a capability the port lacks. Rust names each one in its `PENDING` constant (file, section), prints a skip line, and fails once the section would pass, so landing the capability forces the entry to be removed and the section asserted. Rust currently lists mode parsing and event timestamp validation. Do not delete the vector.

## See also

- [`go/runtime/bus/`](../../go/runtime/bus/)
- [`sdk/experimental/rs/src/bus/`](../../sdk/experimental/rs/src/bus/)
