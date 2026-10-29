# Changelog

All notable changes to `@hop-top/kit` are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.5.0-alpha.4](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.5.0-alpha.3...kit-ts/v0.5.0-alpha.4) (2026-10-29)

The hop-top team is happy to announce Kit's TS SDK 0.5.0-alpha.4. This release includes new features and bug fixes.


### ⚠ BREAKING CHANGES

* **ts:** TS routellm URI base_url / provider base URL now beats ROUTELLM_BASE_URL and extras routellm.base_url.
* **llm:** openai stream caps, truncation error, StreamWithTools
* **ts:** URI api_key outranks LLM_API_KEY; URI base_url/host outrank LLM_BASE_URL; params no longer percent-decoded; empty model parses; parse error texts changed; LLM_API_KEY not lent to routellm, lmstudio or unregistered schemes; llm.yaml now read; an env key no longer reaches a URI-named endpoint trusted only via ANTHROPIC_BASE_URL.
* **ts:** a 403 is now PermissionDeniedError (still instanceof AuthError, message "permission denied ..."); anthropic sends every system message joined instead of only the last
* **ts:** RouteLLMAdapter.stream returns a ToolStream (async iterable) instead of an AsyncGenerator; tokens carry text only, one final done token; errors are typed (AuthError, RateLimitError, ModelError, HTTPStatusError, APIError) instead of LLMError text; 429/5xx are retried; a key over plain http to a remote host is refused.

### Features

* **llm:** gemini StreamWithTools with tool-call and usage stream events ([d9dacad](https://github.com/hop-top/poly-kit/commit/d9dacad263b20945a27fedbf66605b6b6d10e319))
* **llm:** openai stream caps, truncation error, StreamWithTools ([a72eaf2](https://github.com/hop-top/poly-kit/commit/a72eaf2493d4404b45e623401385894d4896f367))
* **ts/llm:** openai stream events and stream_options ([68266c3](https://github.com/hop-top/poly-kit/commit/68266c3ad01aef1e2c28288f6f4beba2a895613f))
* **ts:** accept routellm tier names in model field ([aecd6bb](https://github.com/hop-top/poly-kit/commit/aecd6bb5bffbc49566d46b3f4d0ad87b0f518cf6))
* **ts:** anthropic adapter with tool calling ([ed450ef](https://github.com/hop-top/poly-kit/commit/ed450efef40ec8397090b4dd17587f5e3001c1d4))
* **ts:** llm core for provider adapters ([17c6b96](https://github.com/hop-top/poly-kit/commit/17c6b96c474dae655cd76f23920bc71c7f6a2e87))
* **ts:** llm parseURI, llm.yaml and key resolution as in Go ([50d2f39](https://github.com/hop-top/poly-kit/commit/50d2f3934db7ad18086d4bc19dffe688d1e63b53))
* **ts:** llm stream events, tool_choice, hermetic mode, 403 split, system joining ([41b7051](https://github.com/hop-top/poly-kit/commit/41b705112fe88a0e13b8c0202007563a1716cc2d))
* **ts:** openai adapter; routellm tool calling through it ([13042f0](https://github.com/hop-top/poly-kit/commit/13042f01debc39393a30dbf5a3b134ac360c01d8))
* **ts:** tool-name validation and reversible sanitizing ([e484b1c](https://github.com/hop-top/poly-kit/commit/e484b1cd23b80dff7fe9898b59765161995fd16f))
* **ts:** tool-name validation and reversible sanitizing ([#579](https://github.com/hop-top/poly-kit/issues/579)) ([e484b1c](https://github.com/hop-top/poly-kit/commit/e484b1cd23b80dff7fe9898b59765161995fd16f))
* **ts:** validate and reversibly sanitize llm tool names ([bc18b50](https://github.com/hop-top/poly-kit/commit/bc18b5057f68b2250e1758951adb83011aae9b2d))


### Bug Fixes

* **deps:** npm lockfile advisories ([2950eb5](https://github.com/hop-top/poly-kit/commit/2950eb594c6c0a27846828c82d63b442cad12181))
* **llm:** send anthropic media urls as url sources; media_fetch vectors ([c1600dd](https://github.com/hop-top/poly-kit/commit/c1600dd6ad41a40e10fac7d08b644abbbc8af727))
* **ts/config:** bound alias expansion and nesting in load ([f6dd39a](https://github.com/hop-top/poly-kit/commit/f6dd39a168706e9cd7f4f42fb8a5b4a3b08948f1))
* **ts/config:** merge nested objects across layers ([0e9a7f4](https://github.com/hop-top/poly-kit/commit/0e9a7f41a4304a7507b140e13492e06ee9a0698f))
* **ts:** guard URL media fetches against SSRF ([dd73a22](https://github.com/hop-top/poly-kit/commit/dd73a2282504b7d04ddc3b5b228196750d88bcc6))
* **ts:** routellm base URL precedence matches Go ([16e9cb6](https://github.com/hop-top/poly-kit/commit/16e9cb68283d98090722d3c9109d7f43c64fc7c6))

Full diff: [kit-ts/v0.5.0-alpha.3...kit-ts/v0.5.0-alpha.4](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.5.0-alpha.3...kit-ts/v0.5.0-alpha.4)

## [0.5.0-alpha.3](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.5.0-alpha.2...kit-ts/v0.5.0-alpha.3) (2026-09-05)

The hop-top team is happy to announce Kit's TS SDK 0.5.0-alpha.3. This release includes new features and bug fixes.


### Features

* **sdk:** EXIT_GENERIC and generic error constructor in all SDKs
* **ts:** EXIT_GENERIC and genericError for the exit-1 class
* **ts:** serve --enable/--disable and timeout flags
* **ts:** serve cross-language contract and TypeScript implementation
* **ts:** serve hierarchy and service lifecycle


### Bug Fixes

* **parity:** record and implement serve --enable/--disable and timeout flags

Full diff: [kit-ts/v0.5.0-alpha.2...kit-ts/v0.5.0-alpha.3](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.5.0-alpha.2...kit-ts/v0.5.0-alpha.3)

## [0.5.0-alpha.2](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.5.0-alpha.1...kit-ts/v0.5.0-alpha.2) (2026-09-04)

The hop-top team is happy to announce Kit's TS SDK 0.5.0-alpha.2. This release includes new features and bug fixes.


### ⚠ BREAKING CHANGES

* **ai/llm:** `Request.Temperature` type changes from `float64` to `*float64`. Callers setting a literal must pass a pointer; zero-value construction (unset) keeps behaving as before via nil.

### Features

* merge offline-transport
* merge offline-transport
* **sdk/ts:** add structured error envelope with transience class
* **ts/mcp:** implement MRTR confirmation loop on the modern surface
* **ts:** enforce --offline at the fetch layer
* **ts:** serve dual-spec MCP surface


### Bug Fixes

* **ai/llm:** send explicit zero temperature on the wire
* **output:** collapse header/key split, let --cols reorder
* **output:** emit valid csv from ts in crlf mode
* **output:** reach projection helpers from every render
* **parity:** load verbosity/streams blocks, drop decorative table block
* **ts:** exempt telemetry from `--offline`
* **ts:** re-export registerOutputFlags + dispatch from output subpath
* **ts:** ship root index entry so bare package import resolves

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
