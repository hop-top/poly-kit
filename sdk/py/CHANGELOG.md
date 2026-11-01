# Changelog

## [0.5.0-alpha.10](https://github.com/hop-top/poly-kit/compare/kit-py/v0.5.0-alpha.9...kit-py/v0.5.0-alpha.10) (2026-11-01)

The hop-top team is happy to announce Kit's Python SDK 0.5.0-alpha.10. This release includes new features and bug fixes.


### ⚠ BREAKING CHANGES

* **py/cli:** unannotated destructive-named commands need --confirm; malformed side-effect tags refused; reserved global names raise ValueError
* **py/cli:** unannotated destructive-named commands refused without --confirm
* **py/mcp:** unannotated destructive-named leaves now blocked on remote surfaces

### Features

* **py/cli:** delegation gate + policy engine ([a06fc65](https://github.com/hop-top/poly-kit/commit/a06fc65ddb5f0e9596dcecc2ac92ff36a4aa92a2))
* **py/cli:** wire delegation gate into create_app ([429927b](https://github.com/hop-top/poly-kit/commit/429927be55cb859a54da56d6c4f1c328e55cb6ec))
* **py/mcp:** infer destructive from leaf name in safety gate ([70b764c](https://github.com/hop-top/poly-kit/commit/70b764cfc5c38858af18bf3616bc6e8f4071c05b))


### Bug Fixes

* **py/cli:** leaf-local confirm and max-ops follow Go ([c6674fb](https://github.com/hop-top/poly-kit/commit/c6674fb6f50309be55575de15ffa263e07727f7a))
* **py:** resync vendored parity.json with canonical contract ([105cbb1](https://github.com/hop-top/poly-kit/commit/105cbb18f813c6b57b3b65708435d7e642835222))
* **ts,py/cli:** inferred dry-run plan matches Go, drop stale skip ([b96cba3](https://github.com/hop-top/poly-kit/commit/b96cba3816a8f21fb2f7b1c900a8240439a8b7c0))

Full diff: [kit-py/v0.5.0-alpha.9...kit-py/v0.5.0-alpha.10](https://github.com/hop-top/poly-kit/compare/kit-py/v0.5.0-alpha.9...kit-py/v0.5.0-alpha.10)

## [0.5.0-alpha.9](https://github.com/hop-top/poly-kit/compare/kit-py/v0.5.0-alpha.8...kit-py/v0.5.0-alpha.9) (2026-10-30)

The hop-top team is happy to announce Kit's Python SDK 0.5.0-alpha.9. This release includes maintenance release with bug fixes.


### Bug Fixes

* **py/toolspec:** load flag default/choices, arg variadic, idempotent class ([1bf5661](https://github.com/hop-top/poly-kit/commit/1bf5661125db55af3354a43c7d316ef773bc56bc))

Full diff: [kit-py/v0.5.0-alpha.8...kit-py/v0.5.0-alpha.9](https://github.com/hop-top/poly-kit/compare/kit-py/v0.5.0-alpha.8...kit-py/v0.5.0-alpha.9)

## [0.5.0-alpha.8](https://github.com/hop-top/poly-kit/compare/kit-py/v0.5.0-alpha.7...kit-py/v0.5.0-alpha.8) (2026-10-30)

The hop-top team is happy to announce Kit's Python SDK 0.5.0-alpha.8. This release includes new features and bug fixes.


### Features

* **mcp:** tool_name_separator mount option in py sdk ([fd51fda](https://github.com/hop-top/poly-kit/commit/fd51fdacd1c150d95752d310f61a92d50fb6bed7))
* **py/mcp:** audit mount hook for refusals ([d778df5](https://github.com/hop-top/poly-kit/commit/d778df54950e5653c8de27cde6348e3122a6248c))
* **py/mcp:** positional args via args array ([1137e3a](https://github.com/hop-top/poly-kit/commit/1137e3a5119978f43822dd5d4f5de8d32f041d58))
* **py/mcp:** serve_stdio transport ([dcb8aaa](https://github.com/hop-top/poly-kit/commit/dcb8aaa389bb1027d87acdf41db1ec1c13155f0a))


### Bug Fixes

* **mcp:** no reply to notifications; ping returns {} ([f24c097](https://github.com/hop-top/poly-kit/commit/f24c09796642894a2329a6170a160283a15c38af))
* **py/alias:** default bridge proxies multi-word targets ([886d71a](https://github.com/hop-top/poly-kit/commit/886d71a35e43cbf925894f34b5720277b2016efb))
* **py/alias:** offer default alias proxies in completion ([4ee0678](https://github.com/hop-top/poly-kit/commit/4ee06783f44502753aaab95d7aa5498d520502ff))
* **py/config:** deep-merge layers like other ports ([a7d1700](https://github.com/hop-top/poly-kit/commit/a7d1700d0c430dea01521850db6b970ecc8cab03))
* **py/toolspec:** loader types match go/ts spec ([2af0824](https://github.com/hop-top/poly-kit/commit/2af082460f116b14ab58f5fa3ab3dec67e3133ea))

Full diff: [kit-py/v0.5.0-alpha.7...kit-py/v0.5.0-alpha.8](https://github.com/hop-top/poly-kit/compare/kit-py/v0.5.0-alpha.7...kit-py/v0.5.0-alpha.8)

## [0.5.0-alpha.7](https://github.com/hop-top/poly-kit/compare/kit-py/v0.5.0-alpha.6...kit-py/v0.5.0-alpha.7) (2026-10-29)

The hop-top team is happy to announce Kit's Python SDK 0.5.0-alpha.7. This release includes new features.


### Features

* **py/alias:** bridge_to_click options, validate_alias_name, detect_cycles ([086f154](https://github.com/hop-top/poly-kit/commit/086f1545c3e76b17687a94e59c516d31617aaca9))

Full diff: [kit-py/v0.5.0-alpha.6...kit-py/v0.5.0-alpha.7](https://github.com/hop-top/poly-kit/compare/kit-py/v0.5.0-alpha.6...kit-py/v0.5.0-alpha.7)

## [0.5.0-alpha.6](https://github.com/hop-top/poly-kit/compare/kit-py/v0.5.0-alpha.5...kit-py/v0.5.0-alpha.6) (2026-10-29)

The hop-top team is happy to announce Kit's Python SDK 0.5.0-alpha.6. This release includes new features and bug fixes.


### ⚠ BREAKING CHANGES

* **ts:** TS routellm URI base_url / provider base URL now beats ROUTELLM_BASE_URL and extras routellm.base_url.
* **llm:** openai stream caps, truncation error, StreamWithTools
* **py:** provider base_url on routellm now names a RouteLLM server (http mode) instead of the in-process Controller api_base; set extras api_base for the Controller upstream. parse_router_threshold classifies via parse_model_field: non-numeric suffix -> tier error.
* **py:** Anthropic and OpenAI llm adapters with tool calling
* **py:** Anthropic and OpenAI llm adapters with tool calling ([#583](https://github.com/hop-top/poly-kit/issues/583))

### Features

* **llm:** gemini StreamWithTools with tool-call and usage stream events ([d9dacad](https://github.com/hop-top/poly-kit/commit/d9dacad263b20945a27fedbf66605b6b6d10e319))
* **llm:** openai stream caps, truncation error, StreamWithTools ([a72eaf2](https://github.com/hop-top/poly-kit/commit/a72eaf2493d4404b45e623401385894d4896f367))
* **py/llm:** openai stream events and stream_options ([51ca78c](https://github.com/hop-top/poly-kit/commit/51ca78c4414337dc2cb9ce4102862a0816be8027))
* **py:** Anthropic and OpenAI llm adapters with tool calling ([0c81c43](https://github.com/hop-top/poly-kit/commit/0c81c431208b51160880302481cf269f6d10cd50))
* **py:** Anthropic and OpenAI llm adapters with tool calling ([#583](https://github.com/hop-top/poly-kit/issues/583)) ([0c81c43](https://github.com/hop-top/poly-kit/commit/0c81c431208b51160880302481cf269f6d10cd50))
* **py:** llm anthropic and openai adapters with tool calling ([ddc3ee3](https://github.com/hop-top/poly-kit/commit/ddc3ee342a1b6313b2f2cf01129cd680263a5d03))
* **py:** llm permission-denied error and anthropic system joining ([2781ea0](https://github.com/hop-top/poly-kit/commit/2781ea0037f633f65dcf84633c776f7790a4d2ae))
* **py:** llm tool_choice, hermetic resolve, stream events, metadata-only event payloads ([adae270](https://github.com/hop-top/poly-kit/commit/adae27033e5be229c8547545358ae1367369697f))
* **py:** routellm http mode with tier names ([dc32422](https://github.com/hop-top/poly-kit/commit/dc32422869cf0c55407a449820c270baed316a2d))
* **py:** tool-name validation and reversible sanitizing ([d7563b0](https://github.com/hop-top/poly-kit/commit/d7563b0ac3d6189e8c755c959c47071a1483fdf9))
* **py:** tool-name validation and reversible sanitizing ([#580](https://github.com/hop-top/poly-kit/issues/580)) ([d7563b0](https://github.com/hop-top/poly-kit/commit/d7563b0ac3d6189e8c755c959c47071a1483fdf9))
* **py:** validate and reversibly sanitize llm tool names ([31c4eef](https://github.com/hop-top/poly-kit/commit/31c4eef0e01c502e47b3fe89d14fafcf1a5abe30))


### Bug Fixes

* **deps:** py lockfile advisories ([fec8ef8](https://github.com/hop-top/poly-kit/commit/fec8ef848c691937574bbaed730fc3fa7a9d952c))
* **llm:** send anthropic media urls as url sources; media_fetch vectors ([c1600dd](https://github.com/hop-top/poly-kit/commit/c1600dd6ad41a40e10fac7d08b644abbbc8af727))
* **py:** guard llm URLSource media fetch against SSRF ([ffef3a4](https://github.com/hop-top/poly-kit/commit/ffef3a4309634e8a40d47569ee4d89364aef7e25))
* **py:** judge NAT64 media hosts by their IPv4 ([c779b83](https://github.com/hop-top/poly-kit/commit/c779b830a35ecca93c38f5fe340f7b62048d455d))
* **templates:** cli-py declares kit dependency; sdk/py ships py.typed ([4a490f0](https://github.com/hop-top/poly-kit/commit/4a490f0a75df36508393ebe9ca66d8bcd0e6df53))
* **ts:** routellm base URL precedence matches Go ([16e9cb6](https://github.com/hop-top/poly-kit/commit/16e9cb68283d98090722d3c9109d7f43c64fc7c6))

Full diff: [kit-py/v0.5.0-alpha.5...kit-py/v0.5.0-alpha.6](https://github.com/hop-top/poly-kit/compare/kit-py/v0.5.0-alpha.5...kit-py/v0.5.0-alpha.6)

## [0.5.0-alpha.5](https://github.com/hop-top/poly-kit/compare/kit-py/v0.5.0-alpha.4...kit-py/v0.5.0-alpha.5) (2026-09-05)

The hop-top team is happy to announce Kit's Python SDK 0.5.0-alpha.5. This release includes new features and bug fixes.


### Features

* **py:** EXIT_GENERIC and generic_error for the exit-1 class
* **py:** port serve supervisor and service selector
* **py:** port the serve supervisor and service selector
* **py:** serve --enable/--disable and timeout flags
* **sdk:** EXIT_GENERIC and generic error constructor in all SDKs
* **ts:** serve cross-language contract and TypeScript implementation


### Bug Fixes

* **parity:** record and implement serve --enable/--disable and timeout flags
* **py:** re-sync vendored parity.json after extends change

Full diff: [kit-py/v0.5.0-alpha.4...kit-py/v0.5.0-alpha.5](https://github.com/hop-top/poly-kit/compare/kit-py/v0.5.0-alpha.4...kit-py/v0.5.0-alpha.5)

## [0.5.0-alpha.4](https://github.com/hop-top/poly-kit/compare/kit-py/v0.5.0-alpha.3...kit-py/v0.5.0-alpha.4) (2026-09-04)

The hop-top team is happy to announce Kit's Python SDK 0.5.0-alpha.4. This release includes new features and bug fixes.


### ⚠ BREAKING CHANGES

* **ai/llm:** `Request.Temperature` type changes from `float64` to `*float64`. Callers setting a literal must pass a pointer; zero-value construction (unset) keeps behaving as before via nil.
* **output:** ColumnSpec(header=..., key=...) with differing names now raises ValueError.

### Features

* merge offline-transport
* merge offline-transport
* **py:** enforce `--offline` at the urllib layer
* **py:** model cobra's lazy help-flag registration on the MCP surface
* **py:** port the dual-spec MCP surface
* **sdk/py:** add structured error envelope with transience class


### Bug Fixes

* **ai/llm:** send explicit zero temperature on the wire
* **build:** realign sdk/py uv.lock with declared version
* **output:** drive row projection from ColumnSpec
* **output:** forward ColumnSpec from dispatch to formatters
* **output:** honor ColumnSpec order in tabular formatters
* **output:** pin ColumnSpec header to key
* **output:** project cols in json and yaml formatters
* **output:** quote leading whitespace in py csv fields
* **output:** restore ColumnSpec wiring on py csv formatter
* **py:** pin the clock in sqlstore TTL tests
* **py:** vendor parity.json into package, drop monorepo fs read

Full diff: [kit-py/v0.5.0-alpha.3...kit-py/v0.5.0-alpha.4](https://github.com/hop-top/poly-kit/compare/kit-py/v0.5.0-alpha.3...kit-py/v0.5.0-alpha.4)

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
