# Changelog

All notable changes to `@hop-top/kit` are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.5.0-alpha.2](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.5.0-alpha.1...kit-ts/v0.5.0-alpha.2) (2026-09-04)

The hop-top team is happy to announce Kit's TS SDK 0.5.0-alpha.2. This release includes new features and bug fixes.


### Features

* merge offline-transport
* merge offline-transport
* **sdk/ts:** add structured error envelope with transience class
* **ts/mcp:** implement MRTR confirmation loop on the modern surface
* **ts:** enforce --offline at the fetch layer
* **ts:** serve dual-spec MCP surface


### Bug Fixes

* **output:** collapse header/key split, let --cols reorder
* **output:** emit valid csv from ts in crlf mode
* **output:** reach projection helpers from every render
* **parity:** load verbosity/streams blocks, drop decorative table block
* **ts:** exempt telemetry from `--offline`
* **ts:** re-export registerOutputFlags + dispatch from output subpath
* **ts:** ship root index entry so bare package import resolves


### Refactored

* **cli:** read verbosity/streams values from parity contract
* **output:** resolve column precedence in dispatch, not formatters

Full diff: [kit-ts/v0.5.0-alpha.1...kit-ts/v0.5.0-alpha.2](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.5.0-alpha.1...kit-ts/v0.5.0-alpha.2)

## [Unreleased]

### Changed

- **Output column ordering is now driven by the `ColumnSpec[]` list.** The
  projection helpers already implemented the rule but were unreachable from a
  render: `options.columns` was consumed by `--cols` validation and then
  dropped, while the built-in formatters derived headers from
  `Object.keys(rows[0])`. The list now supplies the default column order, the
  header labels, and the column selection. **User-visible:** callers passing a
  `columns` list whose order differs from their payload's key order will see
  columns reorder, and payload keys absent from the schema stop being emitted.
  Anyone parsing column positions or diffing `--format json` output sees
  different bytes. Precedence is `--cols` (user order always wins; it reorders
  as well as selects), else `ColumnSpec` order, else payload key order.
- `Formatter.render` is **unchanged**. `resolveEffectiveCols` collapses
  `--cols` and the `ColumnSpec` list into one ordered list once, at the
  dispatch layer, and passes it through the existing `cols` argument — so
  third-party formatters keep working and pick up correct ordering with no
  code change. The collapse is sound only because `header === key`.
- **`header` must equal `key`.** The two name the same column: the label, the
  value matched against `--cols`, and the property read off the row. Go cannot
  express a header/key split through its `table:""` struct tags, so no SDK
  may. `key` is retained for source compatibility and must equal `header`.
  `priority` is still accepted and stored but remains ignored outside Go.

### Removed

- `filterColumns` and `buildHeaderToKey`. Under `header === key` the latter
  was an identity map and `projectRows`' `out[c.header] = r[c.key]` an
  identity mapping; `filterColumns` became unreachable once dispatch passes
  the resolved column list verbatim. Unknown-`--cols` rejection was never
  lost — it lives in `validateCols`.

### Known limitations

- `csv` and `text` formatters are not implemented in the Rust and PHP SDKs.
  Only `table`, `json` and `yaml` are portable across all five kit runtimes.

### Fixed

- **CSV no longer emits structurally invalid output in `crlf` mode.** A field
  containing an embedded LF was written UNQUOTED, so a single record decoded
  as two records (three under some readers). This was data corruption, not a
  quoting preference. Encoding is now hand-rolled instead of delegated to
  csv-stringify, which produced that output in its `windows`
  record-delimiter mode.
- **A field beginning with whitespace is now quoted**, matching the other
  SDKs; it was previously emitted bare. A field equal to `\.` is quoted too,
  since that sequence alone on a line terminates a PostgreSQL `COPY` stream.
- A quoted field's bytes are preserved verbatim in both line-ending modes and
  both quoting paths; `crlf` changes the record terminator and nothing else.
  RFC 4180 lists `CR` and `LF` as separate alternatives inside the `escaped`
  production, so a bare CR between quotes is legal.

## [0.5.0-alpha.1](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.5.0-alpha.0...kit-ts/v0.5.0-alpha.1) (2026-07-14)

The hop-top team is happy to announce Kit's TS SDK 0.5.0-alpha.1. This release includes maintenance release with bug fixes.


### Bug Fixes

* **ts:** ESM export conditions + self-contained parity data

Full diff: [kit-ts/v0.5.0-alpha.0...kit-ts/v0.5.0-alpha.1](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.5.0-alpha.0...kit-ts/v0.5.0-alpha.1)

## [0.5.0-alpha.0](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.4.0-alpha.2...kit-ts/v0.5.0-alpha.0) (2026-06-07)

The hop-top team is happy to announce Kit's TS SDK 0.5.0-alpha.0. This release includes miscellaneous improvements.


### Refactored

* migrate uri+hdl to cite across Go/TS/Py/Rs/PHP

Full diff: [kit-ts/v0.4.0-alpha.2...kit-ts/v0.5.0-alpha.0](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.4.0-alpha.2...kit-ts/v0.5.0-alpha.0)

## [0.4.0-alpha.2](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.4.0-alpha.1...kit-ts/v0.4.0-alpha.2) (2026-06-03)

The hop-top team is happy to announce Kit's TS SDK 0.4.0-alpha.2. This release includes new features.


### Features

* **contracts:** typeid-v1 cross-language parity fixtures
* **telemetry:** consenting telemetry stack across kit-go + 4 SDKs
* **ts:** kit-sdk/id — typeid primitive

Full diff: [kit-ts/v0.4.0-alpha.1...kit-ts/v0.4.0-alpha.2](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.4.0-alpha.1...kit-ts/v0.4.0-alpha.2)

## [0.4.0-alpha.1](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.4.0-alpha.0...kit-ts/v0.4.0-alpha.1) (2026-05-17)

The hop-top team is happy to announce Kit's TS SDK 0.4.0-alpha.1. This release includes new features.


### Features

* initial public release

Full diff: [kit-ts/v0.4.0-alpha.0...kit-ts/v0.4.0-alpha.1](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.4.0-alpha.0...kit-ts/v0.4.0-alpha.1)

## [0.2.0-alpha.0](https://github.com/hop-top/poly-kit/compare/sdk/ts/v0.1.0-alpha.0...sdk/ts/v0.2.0-alpha.0) (2026-05-16)

The hop-top team is happy to announce kit 0.2.0-alpha.0. This release includes new features.


### Features

* initial public release

Full diff: [sdk/ts/v0.1.0-alpha.0...sdk/ts/v0.2.0-alpha.0](https://github.com/hop-top/poly-kit/compare/sdk/ts/v0.1.0-alpha.0...sdk/ts/v0.2.0-alpha.0)

## [0.3.0] — 2026-05-01

### Added

- **Formatter / Registry public API** under `@hop-top/kit/output`:
  - `Formatter<T = unknown>` interface with `key`, `extensions`, `options`,
    and `render(out, data, opts, cols)`.
  - `OptionSpec` (string | int | bool | enum) + `parseOptions` helper
    with type coercion, default fill-in, and enum validation.
  - `ColumnSpec` for header/key/priority metadata passed via `dispatch`.
  - `Registry` class — `register` (throws on dup), `override` (replaces),
    `lookup`, `keys`, `formatters`, `extensionMap`.
  - `defaultRegistry` singleton + `newRegistry()` factory.
- **New built-in formatters**:
  - `csv` — RFC 4180 quoting via `csv-stringify`. Options: `delimiter`,
    `no-header`, `quote-all`, `crlf`. Extension `.csv`.
  - `text` — three styles (`kv`, `lines`, `paragraph`) + custom
    `separator`. Extension `.txt`.
- **New flags via Commander**:
  - `--format-opt <kv...>` — repeatable, validated against the active
    formatter's option specs. Bool keys may omit `=value`.
  - `--format-help [fmt]` — list registry or per-formatter options.
  - `--cols`, `--columns <cols...>` — variadic + comma-split + dedupe;
    honored by all five built-ins.
  - `--template <tpl>` — eta engine (EJS-style `<%= %>`); mutually
    exclusive with `--cols`.
  - `-o`, `--output <path>` — write to file. Empty string or `-` =
    stdout. Extension inference selects the format when `--format` is
    default; explicit `--format` paired with a different extension is a
    hard mismatch error.
- **Helper exports** under `@hop-top/kit/output`:
  - `registerOutputFlags(program, opts?)` — wires the full flag suite.
  - `dispatch(cmd, data, opts?)` — resolves writer/format/options/cols
    and invokes the active formatter.
- **CLI factory** (`createCLI`) now wires the full flag suite via
  `registerOutputFlags`. `disable.format` toggles all six output flags.
- **Template scaffold** `templates/cli-ts/src/cli.ts.tmpl` ships the
  parity flag suite by default.

### Changed

- `render(w, format, v)` is now a thin shim over `defaultRegistry`.
  Behavior is unchanged for `json` / `yaml` / `table`.
- New `dependencies`: `csv-stringify` (CSV quoting) and `eta`
  (template engine).

### Backward compatibility

- The existing `render(w, format, v)` signature is preserved.
- The five existing format constants stay (`JSON_FORMAT`, `YAML_FORMAT`,
  `TABLE_FORMAT`); `CSV_FORMAT` and `TEXT_FORMAT` added.
- All existing tests pass byte-for-byte; the only change is
  `output.test.ts`'s "unknown format" fixture, which now uses `'bogus'`
  in place of `'csv'` — `csv` is now a registered built-in.

## [0.2.1] and earlier

See git history.
