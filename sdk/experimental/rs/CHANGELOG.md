# Changelog

## [0.5.0-alpha.1](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.5.0-alpha.0...kit-rs/v0.5.0-alpha.1) (2026-09-04)


### ⚠ BREAKING CHANGES

* **rs:** `telemetry::ClientOptions` gains a `policy` field, so struct-literal construction must add it (`..Default::default()` is unaffected). `api::ApiClient::request` returns `netpolicy::RequestBuilder` instead of `reqwest::RequestBuilder`.

### Features

* **httpcache:** port response cache to rust behind feature gate ([d931f09](https://github.com/hop-top/poly-kit/commit/d931f0920a68c91e8bfbb17665479476b61264dc))
* merge offline-transport ([9c20087](https://github.com/hop-top/poly-kit/commit/9c20087cab95e8006929155d1c59c1a3afb20738))
* merge offline-transport ([6fa2303](https://github.com/hop-top/poly-kit/commit/6fa2303f7aa212d8ec2fb88ee1f200c54b9e2107))
* **output:** add rs csv + text formatters ([72b5596](https://github.com/hop-top/poly-kit/commit/72b5596905cb9c0b4d51fa6f41c05e70a7be809c))
* **rs/sqldb:** configurable WAL retry budget on Options ([fedbe1b](https://github.com/hop-top/poly-kit/commit/fedbe1ba478b49231e11cea7050da2c9824b8d16))
* **rs:** add kv store with sqlite backend ([d6ae336](https://github.com/hop-top/poly-kit/commit/d6ae33628e7dc51c669404f3bf3ef91fee5dced5))
* **rs:** add sqldb primitive ([48c43de](https://github.com/hop-top/poly-kit/commit/48c43dece59377fa62638461cddeed205661af18))
* **rs:** add sqlstore typed kv store over sqldb ([acbf0a6](https://github.com/hop-top/poly-kit/commit/acbf0a673573d6adb2403505096ddb6b4b258885))
* **rs:** enforce `--offline` at the reqwest construction path ([76e413b](https://github.com/hop-top/poly-kit/commit/76e413b39e66d7268bc005503e04fa668403a039))
* **rs:** port blob local backend behind `blob` feature ([1c99c8d](https://github.com/hop-top/poly-kit/commit/1c99c8d64b8437a191967812ee31ecb168314c06))
* **rs:** port bus core behind `bus` feature ([ed0e92f](https://github.com/hop-top/poly-kit/commit/ed0e92f6996ac270a8d797aad2c84d8b2982a13f))
* **rs:** port dual-spec MCP surface behind an `mcp` feature ([32cb217](https://github.com/hop-top/poly-kit/commit/32cb2179d4e58929aeb802fc9be4aa4a953c55aa))
* **rs:** port relative-date parsing behind timeutil feature ([51a8b7b](https://github.com/hop-top/poly-kit/commit/51a8b7ba374d9607e050976edbf0b08bf0f4ddea))
* **rs:** port relative-date parsing behind timeutil feature ([9b755b5](https://github.com/hop-top/poly-kit/commit/9b755b5a5e243ec0e5739d10e26c39e5f5a5c7ef))
* **sdk/rs:** add structured error envelope with transience class ([2857281](https://github.com/hop-top/poly-kit/commit/2857281c9dc592de9f3fcc4677014cb80c20ca89))


### Bug Fixes

* **ci:** mirror-sync false-positives on the documented .go/.tmpl rename ([e3a2ca7](https://github.com/hop-top/poly-kit/commit/e3a2ca7e3b7e6ba928fa023790387906bd23d7ba))
* **output:** decide table emptiness by row count, not column count ([67fdcda](https://github.com/hop-top/poly-kit/commit/67fdcdaed378c42045817d848d90ce4aaf97ff12))
* **output:** enable serde_json preserve_order in rs ([369a729](https://github.com/hop-top/poly-kit/commit/369a7295bd88d41b987708d365989e26e8f98491))
* **output:** enforce header == key on ColumnSpec construction ([2b20770](https://github.com/hop-top/poly-kit/commit/2b2077055600f2d5cef1077593d85931e6a49547))
* **output:** give rs --template an ordered-column affordance ([fa80f93](https://github.com/hop-top/poly-kit/commit/fa80f932a6d1b9d8794f294822c32075914e9fcf))
* **output:** preserve CR and LF verbatim in rs csv fields ([2eac6bf](https://github.com/hop-top/poly-kit/commit/2eac6bf050433dba415040e9f877aafceac2e2d5))
* **output:** thread ColumnSpec order through to formatters ([12599a7](https://github.com/hop-top/poly-kit/commit/12599a785184ad10d90a3278578261e3d221be42))
* **rs/blob:** narrow stored blob mode to 0640 ([af6a8fb](https://github.com/hop-top/poly-kit/commit/af6a8fb1c4821f098cca93109a501efc0b82c0d6))
* **rs:** clippy io_other_error in tests/error.rs, fmt lib.rs ([448757b](https://github.com/hop-top/poly-kit/commit/448757bb46d56d358f6b950a384e8be4397e1a60))
* **rs:** correct restore_from_blob doc comment, no rename fix needed ([e73594b](https://github.com/hop-top/poly-kit/commit/e73594b42b4013e6f9229874141387190289c509))
* **rs:** exempt telemetry from `--offline` ([84bcad8](https://github.com/hop-top/poly-kit/commit/84bcad8befccbefaadb52053eb1ebec63404c26e))
* **rs:** match Go exactly on bare week phrases ([12528e9](https://github.com/hop-top/poly-kit/commit/12528e984fdb5f898abffa335073780c0496a40e))
* **rs:** match Go exactly on bare week phrases ([6db8bc2](https://github.com/hop-top/poly-kit/commit/6db8bc2aded6f2c1b258c7221b47e3d2227e67c4))
* **rs:** reflect lazily-attached leaf flags on long-lived mounts ([24a58d3](https://github.com/hop-top/poly-kit/commit/24a58d3e844a2fb0511d1321e603bca312b53371))
* **rs:** reject empty and leading-slash keys in blob local resolve ([f50e835](https://github.com/hop-top/poly-kit/commit/f50e835dd9bf9c4f32f269a92b87705d8ef5e4a5))
* **rs:** reject every key spelling that resolves to the blob store root ([91e93a1](https://github.com/hop-top/poly-kit/commit/91e93a1b1704f00cf55cfe6befb0042816c2880e))
* **rs:** retry rename-over-existing on restore for Windows parity ([10ef478](https://github.com/hop-top/poly-kit/commit/10ef47835dc64906f7a066f526bf8a02928e887c))
* **rs:** store kv keys as TEXT for cross-language SQLite access ([08a3d17](https://github.com/hop-top/poly-kit/commit/08a3d17c5d2e0c073d45ec6d39ac9adad7d4973f))
* **sqldb:** tolerate concurrent first-open WAL conversion ([ba628ac](https://github.com/hop-top/poly-kit/commit/ba628ac9a1233e7f04352dc98b76ff0255de6c28))

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
