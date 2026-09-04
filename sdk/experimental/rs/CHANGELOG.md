# Changelog

## [0.5.0-alpha.1](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.5.0-alpha.0...kit-rs/v0.5.0-alpha.1) (2026-09-04)

The hop-top team is happy to announce Kit's Rust SDK 0.5.0-alpha.1. This release includes new features and bug fixes.


### ⚠ BREAKING CHANGES

* **rs:** `telemetry::ClientOptions` gains a `policy` field, so struct-literal construction must add it (`..Default::default()` is unaffected). `api::ApiClient::request` returns `netpolicy::RequestBuilder` instead of `reqwest::RequestBuilder`.

### Features

* **httpcache:** port response cache to rust behind feature gate
* merge offline-transport
* merge offline-transport
* **output:** add rs csv + text formatters
* **rs/sqldb:** configurable WAL retry budget on Options
* **rs:** add kv store with sqlite backend
* **rs:** add sqldb primitive
* **rs:** add sqlstore typed kv store over sqldb
* **rs:** enforce `--offline` at the reqwest construction path
* **rs:** port blob local backend behind `blob` feature
* **rs:** port bus core behind `bus` feature
* **rs:** port dual-spec MCP surface behind an `mcp` feature
* **rs:** port relative-date parsing behind timeutil feature
* **rs:** port relative-date parsing behind timeutil feature
* **sdk/rs:** add structured error envelope with transience class


### Bug Fixes

* **ci:** mirror-sync false-positives on the documented .go/.tmpl rename
* **output:** decide table emptiness by row count, not column count
* **output:** enable serde_json preserve_order in rs
* **output:** enforce header == key on ColumnSpec construction
* **output:** give rs --template an ordered-column affordance
* **output:** preserve CR and LF verbatim in rs csv fields
* **output:** thread ColumnSpec order through to formatters
* **rs/blob:** narrow stored blob mode to 0640
* **rs:** clippy io_other_error in tests/error.rs, fmt lib.rs
* **rs:** correct restore_from_blob doc comment, no rename fix needed
* **rs:** exempt telemetry from `--offline`
* **rs:** match Go exactly on bare week phrases
* **rs:** match Go exactly on bare week phrases
* **rs:** reflect lazily-attached leaf flags on long-lived mounts
* **rs:** reject empty and leading-slash keys in blob local resolve
* **rs:** reject every key spelling that resolves to the blob store root
* **rs:** retry rename-over-existing on restore for Windows parity
* **rs:** store kv keys as TEXT for cross-language SQLite access
* **sqldb:** tolerate concurrent first-open WAL conversion

Full diff: [kit-rs/v0.5.0-alpha.0...kit-rs/v0.5.0-alpha.1](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.5.0-alpha.0...kit-rs/v0.5.0-alpha.1)

## [Unreleased]

### Changed

- **`serde_json`'s `preserve_order` feature is now enabled.** This is the
  headline change and it reaches further than the output layer: it swaps the
  backing store of *every* `serde_json::Map` in the build graph from
  `BTreeMap` to `indexmap`, so JSON object keys serialize in insertion order
  instead of alphabetically. **Any consumer that parses, re-serializes or
  diffs JSON through this crate sees different bytes**, not only callers of
  the output formatters. The feature is additive within a Cargo build graph,
  so it also takes effect for any other crate sharing the same `serde_json`
  compilation. It is set on the dev-dependency as well as the optional one,
  so the test build and the shipped build agree about `Map`'s backing store.
  `indexmap` becomes a hard transitive dependency; it was already present via
  `serde_yaml`, and `Cargo.lock` gains one line. Audited before flipping:
  nothing in the crate hashes, signs or golden-compares serialized JSON
  (`sha2` runs only over raw random bytes), and the cross-language cassettes
  canonicalize with sorted keys, so they are unaffected.
- **Output column ordering is now driven by the `ColumnSpec` list.**
  Previously `DispatchOptions::columns` was consumed only to validate
  `--cols` and then dropped, so every formatter fell back to payload key
  order — which, under the old `BTreeMap`, meant **alphabetical**. A caller
  asking for `ID DUE TYPE PRIORITY CATEGORY TITLE` got
  `CATEGORY DUE ID PRIORITY TITLE TYPE` and no `ColumnSpec` list could say
  otherwise. The list now supplies the default column order, the header
  labels, and the column selection. **User-visible:** output that previously
  came out alphabetically now follows the caller's `ColumnSpec` order, and
  payload keys absent from the schema stop being emitted. Anyone parsing
  column positions or diffing `--format json` output sees different bytes.
  Precedence is `--cols` (user order always wins; it reorders as well as
  selects), else `ColumnSpec` order, else payload key order. `table`, `json`
  and `yaml` follow the same rule.
- `Formatter::render()` is **unchanged**. `resolve_effective_cols`
  (`dispatch.rs:161`) collapses `--cols` and the `ColumnSpec` list into one
  ordered list once, at the dispatch layer, and passes it through the
  existing `cols` parameter — so third-party `Formatter` implementations keep
  compiling and pick up correct ordering with no code change. The collapse is
  sound only because `header == key`; a split would have forced `ColumnSpec`
  values through to every formatter.
- **Zero rows emits nothing** from the `table` formatter — not even a bare
  header row. Emptiness is now decided by row count rather than column count.
  This fixes a live bug: `--cols` on an empty payload previously printed a
  bare header row.
- **`ColumnSpec::new` now panics when `header != key`.** The two name the
  same column, so validation and row lookup are one operation on one name. Go
  cannot express a header/key split through its `table:""` struct tags, so no
  SDK may. `priority` is still accepted and stored but remains ignored
  outside Go.

### Known limitations

- `csv` and `text` formatters are not implemented in this SDK. Only `table`,
  `json` and `yaml` are portable across all five kit runtimes, so callers
  cannot assume `--format csv` exists.
- The `--template` path has no ordered-column affordance: the minimal `{key}`
  substituter receives the row but not the schema. Since `--template` and
  `--cols` are mutually exclusive, the `ColumnSpec` list is the only ordering
  signal on that path, and Rust exposes none of it. Go (`.Cols`), Python and
  TS (`cols`) expose an iterable column list; PHP has `{*}`. The spelling for
  Rust is an open decision.

### Fixed

- **CSV fields containing CR or LF are now preserved verbatim.** With
  `crlf` set, the encoder previously DROPPED a lone carriage return and
  rewrote an embedded LF to CRLF inside the quoted field — both silent,
  both irrecoverable, and the CR drop applied on the `quote-all` path too.
  A field holding CR and/or LF is quoted and its bytes pass through
  untouched in both line-ending modes and both quoting paths; `crlf` now
  changes the record terminator and nothing else. RFC 4180 lists `CR` and
  `LF` as separate alternatives inside the `escaped` production, so a bare
  CR between quotes is legal, and W3C CSV on the Web states that line
  endings within escaped cells are not normalised.
  **User-visible:** `--format csv --format-opt crlf` output for values
  containing `\r` changes, and such values now survive a decode round-trip.
- **Leading-whitespace quoting matches the documented rule.** The check was
  `starts_with(' ')`, which left a leading TAB, vertical tab or NBSP
  unquoted; it is now a unicode whitespace test on the first character, as
  the module doc claimed. A field equal to `\.` is also quoted, since that
  sequence alone on a line terminates a PostgreSQL `COPY` stream.

## [0.5.0-alpha.0](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.4.0-experimental.3...kit-rs/v0.5.0-alpha.0) (2026-06-07)


### Refactored

* migrate uri+hdl to cite across Go/TS/Py/Rs/PHP ([#129](https://github.com/hop-top/poly-kit/issues/129)) ([105fbe3](https://github.com/hop-top/poly-kit/commit/105fbe3eefb0e92a4c313479791a3af0477c3cd5))

## [0.4.0-experimental.3](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.4.0-experimental.2...kit-rs/v0.4.0-experimental.3) (2026-05-21)

The hop-top team is happy to announce Kit's Rust SDK 0.4.0-experimental.3. This release includes new features.


### Features

* **contracts:** typeid-v1 cross-language parity fixtures
* **init:** add php & rs templates
* **rs:** kit-sdk/id — typeid primitive
* **telemetry:** consenting telemetry stack across kit-go + 4 SDKs

Full diff: [kit-rs/v0.4.0-experimental.2...kit-rs/v0.4.0-experimental.3](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.4.0-experimental.2...kit-rs/v0.4.0-experimental.3)

## [0.4.0-experimental.2](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.4.0-experimental.1...kit-rs/v0.4.0-experimental.2) (2026-05-17)

The hop-top team is happy to announce Kit's Rust SDK 0.4.0-experimental.2. This release includes maintenance release with bug fixes.


### Bug Fixes

* **sdk/rs:** gate api_test on api feature + wire Rust into PR CI

Full diff: [kit-rs/v0.4.0-experimental.1...kit-rs/v0.4.0-experimental.2](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.4.0-experimental.1...kit-rs/v0.4.0-experimental.2)

## [0.4.0-experimental.1](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.4.0-experimental.0...kit-rs/v0.4.0-experimental.1) (2026-05-17)

The hop-top team is happy to announce Kit's Rust SDK 0.4.0-experimental.1. This release includes new features.


### Features

* initial public release

Full diff: [kit-rs/v0.4.0-experimental.0...kit-rs/v0.4.0-experimental.1](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.4.0-experimental.0...kit-rs/v0.4.0-experimental.1)

## [0.2.0-experimental.0](https://github.com/hop-top/poly-kit/compare/sdk/rs/v0.1.0-experimental.0...sdk/rs/v0.2.0-experimental.0) (2026-05-16)

The hop-top team is happy to announce kit 0.2.0-experimental.0. This release includes new features.


### Features

* initial public release

Full diff: [sdk/rs/v0.1.0-experimental.0...sdk/rs/v0.2.0-experimental.0](https://github.com/hop-top/poly-kit/compare/sdk/rs/v0.1.0-experimental.0...sdk/rs/v0.2.0-experimental.0)
