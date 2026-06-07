# Changelog

## [0.5.0-alpha.0](https://github.com/hop-top/poly-kit/compare/kit-py/v0.4.0-alpha.2...kit-py/v0.5.0-alpha.0) (2026-06-07)

The hop-top team is happy to announce Kit's Python SDK 0.5.0-alpha.0. This release includes miscellaneous improvements.


### Refactored

* migrate uri+hdl to cite across Go/TS/Py/Rs/PHP

Full diff: [kit-py/v0.4.0-alpha.2...kit-py/v0.5.0-alpha.0](https://github.com/hop-top/poly-kit/compare/kit-py/v0.4.0-alpha.2...kit-py/v0.5.0-alpha.0)

## [0.4.0-alpha.2](https://github.com/hop-top/poly-kit/compare/kit-py/v0.4.0-alpha.1...kit-py/v0.4.0-alpha.2) (2026-06-03)

The hop-top team is happy to announce Kit's Python SDK 0.4.0-alpha.2. This release includes new features.


### Features

* **contracts:** typeid-v1 cross-language parity fixtures
* **py:** kit-sdk/id — typeid primitive
* **telemetry:** consenting telemetry stack across kit-go + 4 SDKs

Full diff: [kit-py/v0.4.0-alpha.1...kit-py/v0.4.0-alpha.2](https://github.com/hop-top/poly-kit/compare/kit-py/v0.4.0-alpha.1...kit-py/v0.4.0-alpha.2)

## [0.4.0-alpha.1](https://github.com/hop-top/poly-kit/compare/kit-py/v0.4.0-alpha.0...kit-py/v0.4.0-alpha.1) (2026-05-17)

The hop-top team is happy to announce Kit's Python SDK 0.4.0-alpha.1. This release includes new features.


### Features

* initial public release

Full diff: [kit-py/v0.4.0-alpha.0...kit-py/v0.4.0-alpha.1](https://github.com/hop-top/poly-kit/compare/kit-py/v0.4.0-alpha.0...kit-py/v0.4.0-alpha.1)

## [0.2.0-alpha.0](https://github.com/hop-top/poly-kit/compare/sdk/py/v0.1.0-alpha.0...sdk/py/v0.2.0-alpha.0) (2026-05-16)

The hop-top team is happy to announce kit 0.2.0-alpha.0. This release includes new features.


### Features

* initial public release

## 0.3.0 — 2026-05-01

### Added

- `hop_top_kit.output` package with extensible formatter surface
  matching `hop.top/kit/go/console/output`:
  - `Formatter` (`@runtime_checkable` Protocol), `OptionSpec`,
    `ColumnSpec` frozen dataclasses, `parse_options` helper.
  - `Registry` (`register` / `override` / `lookup` / `keys` /
    `formatters` / `extension_map`), `default_registry` singleton,
    `new_registry()` factory.
  - Built-in formatters: `json`, `yaml`, `table`, **`csv`**, **`text`**
    (kv / lines / paragraph styles).
  - `dispatch(ctx, data, columns=...)` Typer-aware orchestrator.
  - `register_output_flags(app, disable=..., registry=...)` injects the
    full flag suite onto every subcommand: `--format`, `--format-opt`
    (repeatable, validated against per-formatter `OptionSpec`s),
    `--format-help` (catalog or per-format options), `--cols` /
    `--columns` (comma-split + dedupe), `--template` (Jinja2; auto-
    escape off; mutex with `--cols`), `--output` / `-o` (file or `-`
    sentinel for stdout; extension inference; explicit-format
    mismatch error).
- `Jinja2 >= 3.1.6` runtime dependency (drives `--template`).

### Changed

- `Format` Literal extended to `"table" | "json" | "yaml" | "csv" |
  "text"` — extending a Literal is non-breaking for typed adopters.
- `create_app` now calls `register_output_flags(app)` when
  `Disable.format` is False, so subcommands inherit the new flag
  suite without per-command boilerplate.
- `templates/cli-py/src/{{.Name}}/cli.py.tmpl` scaffold updated to
  ship the parity flags + a sample `list` command exercising
  `dispatch()` + `ColumnSpec`.

### Compatibility

- The legacy `render(w, format, v)` signature is preserved verbatim;
  it now delegates to `default_registry.lookup(format).render(...)`.
  Existing `tests/test_output.py` runs unchanged.
- Migration to `dispatch()` is opt-in per adopter.

Full diff: [sdk/py/v0.1.0-alpha.0...sdk/py/v0.2.0-alpha.0](https://github.com/hop-top/poly-kit/compare/sdk/py/v0.1.0-alpha.0...sdk/py/v0.2.0-alpha.0)
