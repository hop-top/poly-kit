# Changelog

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
