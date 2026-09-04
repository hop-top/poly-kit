# Changelog

All notable changes to `@hop-top/kit` are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.5.0-alpha.2](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.5.0-alpha.1...kit-ts/v0.5.0-alpha.2) (2026-09-04)


### ⚠ BREAKING CHANGES

* **ai/llm:** `Request.Temperature` type changes from `float64` to `*float64`. Callers setting a literal must pass a pointer; zero-value construction (unset) keeps behaving as before via nil.

### Features

* merge offline-transport ([9c20087](https://github.com/hop-top/poly-kit/commit/9c20087cab95e8006929155d1c59c1a3afb20738))
* merge offline-transport ([6fa2303](https://github.com/hop-top/poly-kit/commit/6fa2303f7aa212d8ec2fb88ee1f200c54b9e2107))
* **sdk/ts:** add structured error envelope with transience class ([6c17d55](https://github.com/hop-top/poly-kit/commit/6c17d55bdf03c1b4b04dc0f15c663f8b326d4c56))
* **ts/mcp:** implement MRTR confirmation loop on the modern surface ([bd048af](https://github.com/hop-top/poly-kit/commit/bd048af1d85faec746a3816041ce9a553390084c))
* **ts:** enforce --offline at the fetch layer ([baf17b2](https://github.com/hop-top/poly-kit/commit/baf17b2ab53c893533f440cac56806c437bb5cb8))
* **ts:** serve dual-spec MCP surface ([e9f9ada](https://github.com/hop-top/poly-kit/commit/e9f9ada729d20164facffefcf11f86514887223f))


### Bug Fixes

* **ai/llm:** send explicit zero temperature on the wire ([97ef854](https://github.com/hop-top/poly-kit/commit/97ef8547f3ed14d2ec62e9ee55747125fb3d9f0c))
* **output:** collapse header/key split, let --cols reorder ([b11b33c](https://github.com/hop-top/poly-kit/commit/b11b33c4a30e52fb062922c798f8db3ed7541f2a))
* **output:** emit valid csv from ts in crlf mode ([259151e](https://github.com/hop-top/poly-kit/commit/259151e2d6641ca0c1044c2389cb09bc7489aed2))
* **output:** reach projection helpers from every render ([706010c](https://github.com/hop-top/poly-kit/commit/706010c123c1fc49e0d53170ff3a343c4aa302d9))
* **parity:** load verbosity/streams blocks, drop decorative table block ([e94d031](https://github.com/hop-top/poly-kit/commit/e94d031b61a229bb74ac1c6c2d7006aa847f8c4d))
* **ts:** exempt telemetry from `--offline` ([e11a74a](https://github.com/hop-top/poly-kit/commit/e11a74abb16e4a73b1659d9326875f59cade916b))
* **ts:** re-export registerOutputFlags + dispatch from output subpath ([89711a2](https://github.com/hop-top/poly-kit/commit/89711a26b71a012930f91d03e03b7a0b5b4948ad))
* **ts:** ship root index entry so bare package import resolves ([b3155e4](https://github.com/hop-top/poly-kit/commit/b3155e4962f7ea32815e2b816a6680eb522dc514))

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
