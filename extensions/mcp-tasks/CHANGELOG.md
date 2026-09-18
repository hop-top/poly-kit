# Changelog

## [0.1.0-alpha.1](https://github.com/hop-top/poly-kit/compare/mcp-tasks/v0.1.0-alpha.0...mcp-tasks/v0.1.0-alpha.1) (2026-09-18)


### ⚠ BREAKING CHANGES

* **mcpsdk:** the tasks extension module path changes from mcpext.example/tasks to hop.top/mcp-tasks. The old path never resolved for consumers, so only in-repo importers are affected.
* **ai/llm:** `Request.Temperature` type changes from `float64` to `*float64`. Callers setting a literal must pass a pointer; zero-value construction (unset) keeps behaving as before via nil.
* **tasks:** `Extension.Handler` removed; `Extension.Attach` now returns error. Hosts mount the SDK handler directly.

### Bug Fixes

* **ai/llm:** send explicit zero temperature on the wire ([97ef854](https://github.com/hop-top/poly-kit/commit/97ef8547f3ed14d2ec62e9ee55747125fb3d9f0c))
* **mcpsdk:** rename tasks extension to hop.top/mcp-tasks ([9e0ecec](https://github.com/hop-top/poly-kit/commit/9e0ecec18c7ac2c294e2a418c498ff7bcd15e6a1))
* **tasks:** fail closed when StartTask runs without Attach ([9d8f0ff](https://github.com/hop-top/poly-kit/commit/9d8f0ffd645fc1bd9a1b7af30c46e9a9fb526d53))
* **tasks:** route tasks methods through SDK dispatch ([ee27633](https://github.com/hop-top/poly-kit/commit/ee276336e5bb70a0968d361bf10b3a82c2d6bcf4))
