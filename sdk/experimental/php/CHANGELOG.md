# Changelog

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
