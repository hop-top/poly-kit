# Changelog

## [0.4.0-alpha.2](https://github.com/hop-top/poly-kit/compare/kit-rs/v0.4.0-experimental.2...kit-rs/v0.4.0-alpha.2) (2026-05-11)

The hop-top team is renaming Kit's Rust SDK pre-release identifier from `experimental.N` to `alpha.N` for fleet-wide consistency with kit-php, kit-ts, and kit-py.

Cargo accepts both `experimental` and `alpha` as SemVer 2.0 pre-release identifiers, so no consumer is broken by either form. The rename aligns kit-rs with the rest of the Kit fleet, which already ships under `alpha.N` (kit-ts, kit-py shipped at `alpha` from the start; kit-php was migrated in T-0183 because Composer rejected `experimental`). Users tracking the linked-versions group now see a single stability flag across all kit-* packages.

### Bug Fixes

* rename SemVer pre-release identifier `experimental.2` -> `alpha.2` for fleet-wide consistency with kit-php / kit-ts / kit-py (T-0188)

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
