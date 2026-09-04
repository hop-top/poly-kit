# Changelog

## [0.5.0-alpha.4](https://github.com/hop-top/poly-kit/compare/kit-py/v0.5.0-alpha.3...kit-py/v0.5.0-alpha.4) (2026-09-04)


### ⚠ BREAKING CHANGES

* **output:** ColumnSpec(header=..., key=...) with differing names now raises ValueError.

### Features

* merge offline-transport ([9c20087](https://github.com/hop-top/poly-kit/commit/9c20087cab95e8006929155d1c59c1a3afb20738))
* merge offline-transport ([6fa2303](https://github.com/hop-top/poly-kit/commit/6fa2303f7aa212d8ec2fb88ee1f200c54b9e2107))
* **py:** enforce `--offline` at the urllib layer ([537c1f3](https://github.com/hop-top/poly-kit/commit/537c1f3d51b56df28e19af690ee650e14bf0c0bd))
* **py:** model cobra's lazy help-flag registration on the MCP surface ([964e6d8](https://github.com/hop-top/poly-kit/commit/964e6d8d3e541925283a7e76b1e3e8d729eaf6ed))
* **py:** port the dual-spec MCP surface ([01c4671](https://github.com/hop-top/poly-kit/commit/01c467157774bb6393a85d7c8c581dfa80b7bb0e))
* **sdk/py:** add structured error envelope with transience class ([51537c9](https://github.com/hop-top/poly-kit/commit/51537c943353627196d7c91e42c109f790d4fb3f))


### Bug Fixes

* **build:** realign sdk/py uv.lock with declared version ([5ecb554](https://github.com/hop-top/poly-kit/commit/5ecb554af709f78b92ed36027e4c66478aa1e749))
* **output:** drive row projection from ColumnSpec ([bce2349](https://github.com/hop-top/poly-kit/commit/bce2349100a1ba0bdf9ed115766bc5745dfdccda))
* **output:** forward ColumnSpec from dispatch to formatters ([867dcf2](https://github.com/hop-top/poly-kit/commit/867dcf2603957fc61428147393cb251e5f71558d))
* **output:** honor ColumnSpec order in tabular formatters ([a0dfbf6](https://github.com/hop-top/poly-kit/commit/a0dfbf68180f69e8a7155f5d962ae961308e6181))
* **output:** pin ColumnSpec header to key ([ac14eee](https://github.com/hop-top/poly-kit/commit/ac14eee6e6ca00c82b1f5fb0782d15df066fa221))
* **output:** project cols in json and yaml formatters ([6588b2a](https://github.com/hop-top/poly-kit/commit/6588b2ae7c580675d5557600be704f9bc7fd515b))
* **output:** quote leading whitespace in py csv fields ([7262d57](https://github.com/hop-top/poly-kit/commit/7262d575dad7f187a29a9c182d19a0c3068eea68))
* **output:** restore ColumnSpec wiring on py csv formatter ([598459e](https://github.com/hop-top/poly-kit/commit/598459e8e64454cb0ac0f7a46091e40872ddbc33))
* **py:** vendor parity.json into package, drop monorepo fs read ([81ee787](https://github.com/hop-top/poly-kit/commit/81ee787fa02ad0af4d5cf7285c5eea1e5f6368f0))


### Refactored

* **cli:** read verbosity and streams from parity contract ([996a26d](https://github.com/hop-top/poly-kit/commit/996a26d6d42e51f6b602ff220a22b7a2a22a000c))
* **parity:** collapse Python path traversals into one loader ([58f112e](https://github.com/hop-top/poly-kit/commit/58f112e9de27597d04634f629be8783d164c6b84))

## [Unreleased]

### Changed

- **Output column ordering is now driven by the `ColumnSpec` list.**
  Previously the `columns=` list passed to `dispatch()` was consumed only to
  validate `--cols` and then dropped, so the table path fell back to payload
  key order while the `--template` path already honored the schema — one
  module, two answers. The list now supplies the default column order, the
  header labels, and the column selection on every path. **User-visible:**
  callers passing a `columns=` list whose order differs from their payload's
  key order will see columns reorder, and payload keys absent from the schema
  stop being emitted. Anyone parsing column positions or diffing
  `--format json` output sees different bytes. Precedence is `--cols` (user
  order always wins; it reorders as well as selects), else `ColumnSpec` order,
  else payload key order.
- **`--format json --cols name` now projects.** The JSON formatter previously
  ignored `cols` entirely and returned full rows, diverging from every other
  SDK. The YAML formatter had the identical defect and is fixed in the same
  pass. Both now follow the resolved column order, so serialized key order
  matches the table path.
- **Column validation has one source of truth.** `dispatch` validated `--cols`
  against the `ColumnSpec` headers while `filter_columns` re-derived headers
  from row keys, so a `ColumnSpec` naming a column absent from a row passed
  dispatch validation and then raised `ValueError` mid-render. The
  `ColumnSpec` list is now passed into projection rather than re-derived.
- **Zero rows emits nothing** — not even a bare header row. Emptiness is
  decided by row count rather than header count, so a `columns=` list no
  longer resurrects a header for an empty payload.
- **`ColumnSpec` now requires `header == key`**, raising `ValueError` on a
  mismatch. The two name the same column, so validation and row lookup are
  one operation on one name. Go cannot express a header/key split through its
  `table:""` struct tags, so no SDK may. `priority` is still accepted and
  stored but remains ignored outside Go.
- A custom formatter's `render` may now accept an optional `columns`
  argument; dispatch forwards the caller's `ColumnSpec` list to formatters
  that declare it. The four-argument `render(out, data, opts, cols)` form
  keeps working unchanged.

### Known limitations

- `csv` and `text` formatters are not implemented in the Rust and PHP SDKs.
  Only `table`, `json` and `yaml` are portable across all five kit runtimes.

### Fixed

- **CSV quoting no longer keys off the stdlib writer's QUOTE_MINIMAL.** A
  field beginning with whitespace was emitted BARE, unlike every other
  runtime, so ` leading space` round-tripped intact only because the
  reader's `skipinitialspace` happens to default off. Quoting is now decided
  explicitly: the delimiter, a double quote, LF, CR, or a leading unicode
  whitespace character. A field equal to `\.` is also quoted, since that
  sequence alone on a line terminates a PostgreSQL `COPY` stream.
  **User-visible:** `--format csv` output for values with leading whitespace
  gains surrounding quotes, matching the other SDKs byte-for-byte.
- A quoted field's bytes are now guaranteed verbatim in both line-ending
  modes and both quoting paths, with `crlf` changing the record terminator
  and nothing else. RFC 4180 lists `CR` and `LF` as separate alternatives
  inside the `escaped` production, so a bare CR between quotes is legal.

## [0.5.0-alpha.3](https://github.com/hop-top/poly-kit/compare/kit-py/v0.5.0-alpha.2...kit-py/v0.5.0-alpha.3) (2026-06-07)

The hop-top team is happy to announce Kit's Python SDK 0.5.0-alpha.3. This release includes maintenance release with bug fixes.


### Bug Fixes

* **sdk/py:** pin typer&lt;0.25 for click 8.4 compat

Full diff: [kit-py/v0.5.0-alpha.2...kit-py/v0.5.0-alpha.3](https://github.com/hop-top/poly-kit/compare/kit-py/v0.5.0-alpha.2...kit-py/v0.5.0-alpha.3)

## [0.5.0-alpha.2](https://github.com/hop-top/poly-kit/compare/kit-py/v0.5.0-alpha.1...kit-py/v0.5.0-alpha.2) (2026-06-07)

The hop-top team is happy to announce Kit's Python SDK 0.5.0-alpha.2. This release includes maintenance release with bug fixes.


### Bug Fixes

* **sdk/py:** add click as explicit dev dep, drop stale typer[all]

Full diff: [kit-py/v0.5.0-alpha.1...kit-py/v0.5.0-alpha.2](https://github.com/hop-top/poly-kit/compare/kit-py/v0.5.0-alpha.1...kit-py/v0.5.0-alpha.2)

## [0.5.0-alpha.1](https://github.com/hop-top/poly-kit/compare/kit-py/v0.5.0-alpha.0...kit-py/v0.5.0-alpha.1) (2026-06-07)

The hop-top team is happy to announce Kit's Python SDK 0.5.0-alpha.1. This release includes miscellaneous improvements.


### Refactored

* **sdk/py:** drop unreachable hop_top_cite backend fallback

Full diff: [kit-py/v0.5.0-alpha.0...kit-py/v0.5.0-alpha.1](https://github.com/hop-top/poly-kit/compare/kit-py/v0.5.0-alpha.0...kit-py/v0.5.0-alpha.1)

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
