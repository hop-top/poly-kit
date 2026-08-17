# Changelog

## [Unreleased]

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
