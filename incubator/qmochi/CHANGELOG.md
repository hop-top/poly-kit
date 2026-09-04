# Changelog

## [0.2.0-alpha.1](https://github.com/hop-top/poly-kit/compare/qmochi/v0.2.0-alpha.0...qmochi/v0.2.0-alpha.1) (2026-09-04)

The hop-top team is happy to announce Qmochi 0.2.0-alpha.1. This release includes maintenance release with bug fixes.


### ⚠ BREAKING CHANGES

* **ai/llm:** `Request.Temperature` type changes from `float64` to `*float64`. Callers setting a literal must pass a pointer; zero-value construction (unset) keeps behaving as before via nil.

### Bug Fixes

* **ai/llm:** send explicit zero temperature on the wire

Full diff: [qmochi/v0.2.0-alpha.0...qmochi/v0.2.0-alpha.1](https://github.com/hop-top/poly-kit/compare/qmochi/v0.2.0-alpha.0...qmochi/v0.2.0-alpha.1)

## [0.2.0-alpha.0](https://github.com/hop-top/poly-kit/compare/qmochi/v0.1.0-alpha.0...qmochi/v0.2.0-alpha.0) (2026-05-16)

The hop-top team is happy to announce kit 0.2.0-alpha.0. This release includes new features.


### Features

* initial public release

Full diff: [qmochi/v0.1.0-alpha.0...qmochi/v0.2.0-alpha.0](https://github.com/hop-top/poly-kit/compare/qmochi/v0.1.0-alpha.0...qmochi/v0.2.0-alpha.0)
