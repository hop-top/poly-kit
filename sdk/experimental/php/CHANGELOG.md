# Changelog

## [0.4.0-alpha.2](https://github.com/hop-top/poly-kit/compare/kit-php/v0.4.0-alpha.1...kit-php/v0.4.0-alpha.2) (2026-05-23)


### Features

* **contracts:** typeid-v1 cross-language parity fixtures ([ee7ecfb](https://github.com/hop-top/poly-kit/commit/ee7ecfbc7d382095c18090b956d947b145f919ee))
* **init:** add php & rs templates ([35459b6](https://github.com/hop-top/poly-kit/commit/35459b6e6f586bed3310d5acd5a06f18dd8129e9))
* initial public release ([#1](https://github.com/hop-top/poly-kit/issues/1)) ([12569d0](https://github.com/hop-top/poly-kit/commit/12569d0e12bd0ee97fb1cf9ee835b35b5eab0732))
* **php:** kit-sdk/Id — typeid primitive ([0c92f7d](https://github.com/hop-top/poly-kit/commit/0c92f7d89c2f3884d356c2ff80f06745a111e324))
* **telemetry:** consenting telemetry stack across kit-go + 4 SDKs ([d7d85dc](https://github.com/hop-top/poly-kit/commit/d7d85dce02e64c4bd6bcc4a424810d2dcc9c8fd6))


### Bug Fixes

* **sdk/php:** rename SemVer pre-release identifier experimental.1 -&gt; alpha.1 (T-0183) ([#49](https://github.com/hop-top/poly-kit/issues/49)) ([0b76224](https://github.com/hop-top/poly-kit/commit/0b76224d2c45f98b08591edc805c106b0c38d4c1))

## [Unreleased]

### Added

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
