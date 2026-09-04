# Changelog

## [0.5.0-alpha.1](https://github.com/hop-top/poly-kit/compare/kit-php/v0.5.0-alpha.0...kit-php/v0.5.0-alpha.1) (2026-09-04)


### ⚠ BREAKING CHANGES

* **php:** `HttpsSink` no longer follows redirects; a 3xx from the ingestor now drops the batch instead of trailing the `Location` header. `ApiClient` refuses redirects to plain http and preserves method/body across 301/302.
* **output:** callers passing a ColumnSpec list whose order differs from payload key order see columns reorder; payload keys absent from the schema stop being emitted.
* **output:** ColumnSpec with header != key now throws InvalidArgumentException at construction.

### Features

* merge offline-transport ([9c20087](https://github.com/hop-top/poly-kit/commit/9c20087cab95e8006929155d1c59c1a3afb20738))
* merge offline-transport ([6fa2303](https://github.com/hop-top/poly-kit/commit/6fa2303f7aa212d8ec2fb88ee1f200c54b9e2107))
* **output:** add php csv + text formatters ([3423342](https://github.com/hop-top/poly-kit/commit/3423342d1f8bb82195d2371daf28a7845951d7a2))
* **php:** enforce `--offline` beneath Guzzle and PSR-18 ([ade8dcc](https://github.com/hop-top/poly-kit/commit/ade8dcc8dd545922df16b6e88bb9f3c216d7ea1b))
* **php:** host the MCP surface on PSR-15 and gate confirmations via MRTR ([5b98d59](https://github.com/hop-top/poly-kit/commit/5b98d592307fed9c37a23d2ee96d58dce4b8baa5))
* **php:** serve the dual-spec MCP surface ([a7e6461](https://github.com/hop-top/poly-kit/commit/a7e6461dc990ff2245357d1f37f3af0a9ef9fd2f))
* **sdk/php:** add structured error envelope with transience class ([bfdb725](https://github.com/hop-top/poly-kit/commit/bfdb725a56a00bf2adee3332507c0fd097e64636))


### Bug Fixes

* **build:** realign php composer.lock, guard drift in CI ([cd9faed](https://github.com/hop-top/poly-kit/commit/cd9faedfc0337dec4c0c94736680abad9e893838))
* **deps:** clear 16 php advisories via in-range lock update ([ed35987](https://github.com/hop-top/poly-kit/commit/ed35987b4ab074d8750183a835d267408367c9cc))
* **output:** enforce header == key on php ColumnSpec ([2b388e0](https://github.com/hop-top/poly-kit/commit/2b388e0ecd2236a5e8423deb32381e7a6f3f673a))
* **output:** preserve CR and LF verbatim in php csv fields ([4a8af85](https://github.com/hop-top/poly-kit/commit/4a8af851cc559a7f46a3c1f8bd621c9336c0c324))
* **output:** thread ColumnSpec to php formatters, honor its order ([cc265e6](https://github.com/hop-top/poly-kit/commit/cc265e620477b4eb68b2b6e8175090ef5591d7e4))
* **php:** correct types flagged by static analysis ([44bbd01](https://github.com/hop-top/poly-kit/commit/44bbd01279c2ffae11028f927f1f96508e8b0800))
* **php:** stop guzzle redirects leaking request bodies ([02bfcee](https://github.com/hop-top/poly-kit/commit/02bfceed0d8486070645d702fa613932a090f4bb))
* **telemetry:** report unsupported KIT_TELEMETRY_SINK values ([b7421c3](https://github.com/hop-top/poly-kit/commit/b7421c3e07dab06dbb8787abc2c96d2178323836))


### Refactored

* **output:** resolve effective cols in dispatch, keep Formatter arity ([1d229bd](https://github.com/hop-top/poly-kit/commit/1d229bd91f23c88cdbe5a7d169dd07001964acaa))

## [0.5.0-alpha.0](https://github.com/hop-top/poly-kit/compare/kit-php/v0.4.0-alpha.2...kit-php/v0.5.0-alpha.0) (2026-06-07)

The hop-top team is happy to announce Kit's PHP SDK 0.5.0-alpha.0. This release includes miscellaneous improvements.


### Refactored

* migrate uri+hdl to cite across Go/TS/Py/Rs/PHP

Full diff: [kit-php/v0.4.0-alpha.2...kit-php/v0.5.0-alpha.0](https://github.com/hop-top/poly-kit/compare/kit-php/v0.4.0-alpha.2...kit-php/v0.5.0-alpha.0)

## [0.4.0-alpha.2](https://github.com/hop-top/poly-kit/compare/kit-php/v0.4.0-alpha.1...kit-php/v0.4.0-alpha.2) (2026-05-23)

The hop-top team is happy to announce Kit's PHP SDK 0.4.0-alpha.2. This release includes new features and bug fixes.


### Features

* **contracts:** typeid-v1 cross-language parity fixtures
* **init:** add php & rs templates
* initial public release
* **php:** kit-sdk/Id — typeid primitive
* **telemetry:** consenting telemetry stack across kit-go + 4 SDKs


### Bug Fixes

* **sdk/php:** rename SemVer pre-release identifier experimental.1 -&gt; alpha.1 (T-0183)

Full diff: [kit-php/v0.4.0-alpha.1...kit-php/v0.4.0-alpha.2](https://github.com/hop-top/poly-kit/compare/kit-php/v0.4.0-alpha.1...kit-php/v0.4.0-alpha.2)

## [Unreleased]

### Changed

- **Output column ordering is now driven by the `ColumnSpec` list.** Previously
  the schema passed to `Dispatcher::dispatch()` / `KitCommand::render()` /
  `KitOutput::columns()` was consumed only to validate `--cols` and then
  dropped; every formatter fell back to payload key order. It now supplies the
  default column order, the header labels, and the column *selection*.
  **User-visible:** callers that already pass a `ColumnSpec` list whose order
  differs from their payload key order will see columns reorder, and payload
  keys absent from the schema will stop being emitted. Precedence is `--cols`
  (user order always wins, it reorders as well as selects), else `ColumnSpec`
  order, else payload key order. Applies to the `table`, `json`, and `yaml`
  built-ins alike, so `json`/`yaml` serialized key order follows the same rule.
  `Formatter::render()` is unchanged — the Dispatcher collapses `--cols` and
  the `ColumnSpec` list into one ordered list and passes it through the
  existing `$cols` parameter, so **third-party formatters keep working and
  pick up correct ordering with no code change**. This collapse is sound only
  because `header === key`; a split would have forced the `ColumnSpec` objects
  themselves through to every formatter.
- **`--template` honors the schema too.** The minimal renderer gained a `{*}`
  placeholder that expands to every resolved column's value, tab-separated, in
  schema order — the counterpart of the ordered `cols` variable the Python
  SDK's Jinja path exposes. Plain `{key}` substitution is unchanged.
- **Zero rows emits nothing** from the `table` formatter — not even a bare
  header row. Emptiness is decided by row count, never by column count. This
  fixes a live bug: an empty payload previously emitted a stray blank line,
  and `--cols name,count` on an empty payload printed a bare `name  count`
  header row. A `ColumnSpec` list would have triggered the same lone header
  line once it became a header source.
- **`ColumnSpec` now requires `header === key`**, enforced in the constructor,
  which throws `InvalidArgumentException` on a mismatch. Validation and row
  lookup are one operation on one name. Go cannot express a header/key split
  through its `table:""` struct tags, so no SDK may. `priority` is still
  accepted and stored but remains ignored outside Go.

### Known limitations

- `csv` and `text` formatters are not implemented in this SDK. Only `table`,
  `json` and `yaml` are portable across all five kit runtimes, so callers
  cannot assume `--format csv` exists.
- `{*}` is a PHP-only spelling for ordered columns on the template path. Go
  exposes `.Cols` and Python and TS expose `cols` — iterable column *names* —
  whereas `{*}` yields pre-joined row *values*. The shared spelling for the
  minimal-renderer tier is undecided.

### Fixed

- **CSV fields containing CR or LF are now preserved verbatim.** With `crlf`
  set, the encoder previously DROPPED a lone carriage return and rewrote an
  embedded LF to CRLF inside the quoted field — both silent, both
  irrecoverable, and the CR drop applied on the `quote-all` path too. A field
  holding CR and/or LF is quoted and its bytes pass through untouched in both
  line-ending modes and both quoting paths; `crlf` now changes the record
  terminator and nothing else. RFC 4180 lists `CR` and `LF` as separate
  alternatives inside the `escaped` production, so a bare CR between quotes
  is legal, and W3C CSV on the Web states that line endings within escaped
  cells are not normalised.
  **User-visible:** csv output for values containing `\r` changes under the
  `crlf` option, and such values now survive a `str_getcsv` round-trip.
- **Leading-whitespace quoting matches the documented rule.** The check was
  `str_starts_with($field, ' ')`, which left a leading TAB, vertical tab or
  NBSP unquoted; it is now a unicode whitespace test on the first character,
  as the class docblock claimed. A field equal to `\.` is also quoted, since
  that sequence alone on a line terminates a PostgreSQL `COPY` stream.

### Added

- `HopTop\Kit\Output\Formatter\Projection` — shared column-resolution and
  row-projection helpers used by both the Dispatcher and the built-in
  formatters, so no two of them can drift apart.
  `Projection::resolveEffectiveCols()` is the single home of the precedence
  rule.
- Telemetry module under `HopTop\Kit\Telemetry`:
  - `Mode` enum, env-precedence resolver, `install_id` sharing, consent reader.
  - `JsonlSink` (default; FPM-safe via `register_shutdown_function`).
  - `HttpsSink` (opt-in; Guzzle-backed; FPM block-on-flush caveat documented).
  - `NullSink` (selected by `KIT_TELEMETRY_SINK=none`).
  - Best-effort `Redactor` (email, IPv4/IPv6, `$HOME`, token prefixes) with
    custom-callback escape hatch.
  - `Telemetry` facade now wires consent gating, mode-aware envelope shaping,
    redaction, and sink selection via `KIT_TELEMETRY_SINK`.
- PHP SDK is publish-only (no bus consumer).

## [0.4.0-alpha.1](https://github.com/hop-top/poly-kit/compare/kit-php/v0.4.0-experimental.1...kit-php/v0.4.0-alpha.1) (2026-05-11)

The hop-top team is renaming Kit's PHP SDK pre-release identifier from `experimental.N` to `alpha.N`.

Composer's version parser rejects `experimental` as a pre-release identifier — its recognized stability list is `dev | alpha | beta | RC | stable` — so any downstream PHP consumer requiring `hop-top/kit:0.4.0-experimental.1` failed `composer install` with `Invalid version string`. Renaming to `alpha.1` unblocks `composer install` (T-0183).

Other Kit SDKs (kit-rs, kit-ts, kit-py) keep `experimental.N` — Cargo, npm, and PyPI accept it under strict SemVer 2.0.

### Bug Fixes

* rename SemVer pre-release identifier `experimental.1` -> `alpha.1` so Composer can parse the version constraint (T-0183)

## [0.4.0-experimental.1](https://github.com/hop-top/poly-kit/compare/kit-php/v0.4.0-experimental.0...kit-php/v0.4.0-experimental.1) (2026-05-17)

The hop-top team is happy to announce Kit's PHP SDK 0.4.0-experimental.1. This release includes new features.


### Features

* initial public release

Full diff: [kit-php/v0.4.0-experimental.0...kit-php/v0.4.0-experimental.1](https://github.com/hop-top/poly-kit/compare/kit-php/v0.4.0-experimental.0...kit-php/v0.4.0-experimental.1)

## [0.2.0-experimental.0](https://github.com/hop-top/poly-kit/compare/sdk/php/v0.1.0-experimental.0...sdk/php/v0.2.0-experimental.0) (2026-05-16)

The hop-top team is happy to announce kit 0.2.0-experimental.0. This release includes new features.


### Features

* initial public release

Full diff: [sdk/php/v0.1.0-experimental.0...sdk/php/v0.2.0-experimental.0](https://github.com/hop-top/poly-kit/compare/sdk/php/v0.1.0-experimental.0...sdk/php/v0.2.0-experimental.0)
