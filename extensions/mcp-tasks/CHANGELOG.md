# Changelog

## [0.1.0-alpha.2](https://github.com/hop-top/poly-kit/compare/mcp-tasks/v0.1.0-alpha.1...mcp-tasks/v0.1.0-alpha.2) (2026-10-01)


### ⚠ BREAKING CHANGES

* **deps:** otelhttp 0.70 emits only stable HTTP semconv. Scraped names change: `http_server_duration_milliseconds` -> `http_server_request_duration_seconds`, `http_server_request_size_bytes_total` / `http_server_response_size_bytes_total` -> `http_server_request_body_size_bytes` / `http_server_response_body_size_bytes` histograms. `OTEL_SEMCONV_STABILITY_OPT_IN` no longer applies. Migration: move dashboards and alerts to the new names.

### Build

* **deps:** bump Go modules; otel 1.46, otelhttp stable HTTP metrics ([c0a2b21](https://github.com/hop-top/poly-kit/commit/c0a2b215c065cf1716271634957ac60e591b66f5))

## [0.1.0-alpha.1](https://github.com/hop-top/poly-kit/compare/mcp-tasks/v0.1.0-alpha.0...mcp-tasks/v0.1.0-alpha.1) (2026-09-18)

The hop-top team is happy to announce Kit 0.1.0-alpha.1. This release includes maintenance release with bug fixes.


### ⚠ BREAKING CHANGES

* **mcpsdk:** the tasks extension module path changes from mcpext.example/tasks to hop.top/mcp-tasks. The old path never resolved for consumers, so only in-repo importers are affected.
* **ai/llm:** `Request.Temperature` type changes from `float64` to `*float64`. Callers setting a literal must pass a pointer; zero-value construction (unset) keeps behaving as before via nil.
* **tasks:** `Extension.Handler` removed; `Extension.Attach` now returns error. Hosts mount the SDK handler directly.

### Bug Fixes

* **ai/llm:** send explicit zero temperature on the wire
* **mcpsdk:** rename tasks extension to hop.top/mcp-tasks
* **tasks:** fail closed when StartTask runs without Attach
* **tasks:** route tasks methods through SDK dispatch

Full diff: [mcp-tasks/v0.1.0-alpha.0...mcp-tasks/v0.1.0-alpha.1](https://github.com/hop-top/poly-kit/compare/mcp-tasks/v0.1.0-alpha.0...mcp-tasks/v0.1.0-alpha.1)
