# Changelog

## [0.2.0-alpha.2](https://github.com/hop-top/poly-kit/compare/qmochi/v0.2.0-alpha.1...qmochi/v0.2.0-alpha.2) (2026-10-01)


### ⚠ BREAKING CHANGES

* **deps:** otelhttp 0.70 emits only stable HTTP semconv. Scraped names change: `http_server_duration_milliseconds` -> `http_server_request_duration_seconds`, `http_server_request_size_bytes_total` / `http_server_response_size_bytes_total` -> `http_server_request_body_size_bytes` / `http_server_response_body_size_bytes` histograms. `OTEL_SEMCONV_STABILITY_OPT_IN` no longer applies. Migration: move dashboards and alerts to the new names.

### Build

* **deps:** bump Go modules; otel 1.46, otelhttp stable HTTP metrics ([c0a2b21](https://github.com/hop-top/poly-kit/commit/c0a2b215c065cf1716271634957ac60e591b66f5))

## [0.2.0-alpha.1](https://github.com/hop-top/poly-kit/compare/qmochi/v0.2.0-alpha.0...qmochi/v0.2.0-alpha.1) (2026-09-05)

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
