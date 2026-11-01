# Changelog

All notable changes to `@hop-top/kit` are documented in this file.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/);
versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.5.0-alpha.11](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.5.0-alpha.10...kit-ts/v0.5.0-alpha.11) (2026-11-01)


### ⚠ BREAKING CHANGES

* **ts/cli:** unannotated leaf named delete|remove|rm|destroy|purge|drop now needs confirmation on CLI and is blocked on MCP default policy; --dry-run/--confirm/--max-ops/--policy reserved on root

### Features

* **ts/cli:** delegation gate, side-effect inference ([692582a](https://github.com/hop-top/poly-kit/commit/692582ad6bd12757b5fa9199266a5e0584c108eb))


### Bug Fixes

* **toolspec:** dry_run_supported counts inferred destructive leaves ([8cb1f6c](https://github.com/hop-top/poly-kit/commit/8cb1f6c60bca7033fd9a137113eb3851bc94b2bb))
* **ts,py/cli:** inferred dry-run plan matches Go, drop stale skip ([b96cba3](https://github.com/hop-top/poly-kit/commit/b96cba3816a8f21fb2f7b1c900a8240439a8b7c0))
* **ts/cli:** refuse malformed side-effect tiers before running ([582dc3a](https://github.com/hop-top/poly-kit/commit/582dc3a86e292a1d9ff6ce1bc2576a4559545c80))
* **ts/completion:** deep alias shim offers inherited root flags ([f79acac](https://github.com/hop-top/poly-kit/commit/f79acac4b1a5fe9004292e3e81a25cc139da1d94))

## [0.5.0-alpha.10](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.5.0-alpha.9...kit-ts/v0.5.0-alpha.10) (2026-10-30)

The hop-top team is happy to announce Kit's TS SDK 0.5.0-alpha.10. This release includes new features.


### Features

* **ts/toolspec:** flag default/choices, arg variadic, idempotent class; manifest 1.2 ([80dad00](https://github.com/hop-top/poly-kit/commit/80dad008ac50636cef453ccc3f33457d988b3fcb))

Full diff: [kit-ts/v0.5.0-alpha.9...kit-ts/v0.5.0-alpha.10](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.5.0-alpha.9...kit-ts/v0.5.0-alpha.10)

## [0.5.0-alpha.9](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.5.0-alpha.8...kit-ts/v0.5.0-alpha.9) (2026-10-30)

The hop-top team is happy to announce Kit's TS SDK 0.5.0-alpha.9. This release includes new features and bug fixes.


### Features

* **mcp:** toolNameSeparator mount option in ts sdk ([0d0111c](https://github.com/hop-top/poly-kit/commit/0d0111c4600f7ac6be7fe013f96f757230f90467))
* **ts/mcp:** audit mount hook for refusals ([1f63a5f](https://github.com/hop-top/poly-kit/commit/1f63a5f89cd7ba8c25345ed9478a4a753f5bef28))
* **ts/mcp:** serveStdio transport ([7ab69a6](https://github.com/hop-top/poly-kit/commit/7ab69a622cc1f3c07b7fb6e51234235a5a992b2f))
* **ts/toolspec:** skip builtins, includeHidden in toolSpecFromCommand ([2962a7c](https://github.com/hop-top/poly-kit/commit/2962a7cfac4244d5c3bcdc40bceba24dac92f694))


### Bug Fixes

* **mcp:** no reply to notifications; ping returns {} ([f24c097](https://github.com/hop-top/poly-kit/commit/f24c09796642894a2329a6170a160283a15c38af))
* **ts/mcp:** negated, repeatable option types and default/enum in bridge schema ([863a448](https://github.com/hop-top/poly-kit/commit/863a448e899fc8e6d66f675604fc9427ce6d9b80))
* **ts/mcp:** publish positional args as the Go "args" array ([c47db88](https://github.com/hop-top/poly-kit/commit/c47db88b6f3e7e2594ee103cd87375d19f518e13))
* **ts/toolspec:** negatable flags typed bool ([79cfeb4](https://github.com/hop-top/poly-kit/commit/79cfeb4cc0b31d449b2b23487949dffce518f63d))

Full diff: [kit-ts/v0.5.0-alpha.8...kit-ts/v0.5.0-alpha.9](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.5.0-alpha.8...kit-ts/v0.5.0-alpha.9)

## [0.5.0-alpha.8](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.5.0-alpha.7...kit-ts/v0.5.0-alpha.8) (2026-10-29)

The hop-top team is happy to announce Kit's TS SDK 0.5.0-alpha.8. This release includes new features and bug fixes.


### Features

* **ts/alias:** bridgeAliases options, validateAliasName, detectCycles ([a7c279f](https://github.com/hop-top/poly-kit/commit/a7c279f3abc08ac97dc1f69ce750e0a0e12c88fc))


### Bug Fixes

* **ts/alias:** skip root-alias completion for real-command proxies ([0dc2251](https://github.com/hop-top/poly-kit/commit/0dc225176ed2a1a94fdb8beb4063b56da23ce05c))

Full diff: [kit-ts/v0.5.0-alpha.7...kit-ts/v0.5.0-alpha.8](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.5.0-alpha.7...kit-ts/v0.5.0-alpha.8)

## [0.5.0-alpha.7](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.5.0-alpha.6...kit-ts/v0.5.0-alpha.7) (2026-10-29)

The hop-top team is happy to announce Kit's TS SDK 0.5.0-alpha.7. This release includes new features and bug fixes.


### ⚠ BREAKING CHANGES

* **ts/completion:** generateBashCompletion/Zsh/Fish now return cobra's dynamic scripts, which call `<prog> __complete` (static word lists, `_<prog>_completions` and `-d` descriptions are gone); scripts from the old version must be regenerated. completion leaves take --no-descriptions and include powershell; leaf descriptions follow cobra. The undocumented `(cmd as any).__completionRegistry` hook was never read; use bindFlag/bindArgs (CompletionRegistry stays a plain lookup table, as in Go and Rust).
* **ts/completion:** @hop-top/kit/completion-values behaviour changes: fileCompleter returns its extensions (was a `__file__` marker item) and dirCompleter returns no items (was `__dir__`); both carry a fileFilter the bridge turns into directives 8/16 (no extensions: 0). prefixedCompleter hint description is `<dim> values` (was `<dim>:...`), dimension matched case-insensitively, non-matching prefix yields []. configKeysCompleter returns lowercased leaf keys only (was every intermediate key, case kept).

### Features

* **ts/completion:** align completers with completion-v1 ([7b1445e](https://github.com/hop-top/poly-kit/commit/7b1445ee356e3980f0ac3c0feca9dc0144e7d85b))
* **ts/completion:** cobra scripts and __complete engine ([be7ca8c](https://github.com/hop-top/poly-kit/commit/be7ca8c4063fe6f21706b4c1cdf8d5a52a793724))
* **ts/config:** export mergeLayers, merge policies, load parsers ([1db83db](https://github.com/hop-top/poly-kit/commit/1db83dbd7ec6dd3e704728b63b609881e960b469))
* **ts/config:** wildcard segments in arrayPolicyByPath ([bfa69c3](https://github.com/hop-top/poly-kit/commit/bfa69c37ba3d79fa81bc149137dab96fc5ed4d94))
* **ts/log:** custom formatter hook ([72f935e](https://github.com/hop-top/poly-kit/commit/72f935ed5bc3db79445ccf1cbc2b9680de872f6a))
* **ts/log:** injectable output sink for logger ([f0bf7b0](https://github.com/hop-top/poly-kit/commit/f0bf7b00550217269c66afbd7c37c953e959fe91))
* **ts/log:** json format option ([80941d8](https://github.com/hop-top/poly-kit/commit/80941d8363ea379e42befdcf1ad5c6cd3cdbf58e))
* **ts/log:** level option with silent ([82bb97b](https://github.com/hop-top/poly-kit/commit/82bb97b4b3c0769eac678f3c3cdac13b9ed04326))
* **ts/log:** success method at info level ([f1faa1d](https://github.com/hop-top/poly-kit/commit/f1faa1d7733aba31300106f4c8c29547b526c15c))
* **ts/toolspec:** build ToolSpec from commander tree ([027a37c](https://github.com/hop-top/poly-kit/commit/027a37c12d478061785d7d26a32537f6cebebd9d))
* **ts/toolspec:** carry exit codes on Command ([27f3213](https://github.com/hop-top/poly-kit/commit/27f321372e38df26114107f7f7823284cb107551))
* **ts/toolspec:** port Go manifest builder with byte parity ([db30af7](https://github.com/hop-top/poly-kit/commit/db30af72d64addbb934b378bae23271e6b0abdd7))
* **ts/toolspec:** port spec and spec coverage commands ([979abcd](https://github.com/hop-top/poly-kit/commit/979abcd85c872c9dca15d2c275eee149f2dfdb4e))


### Bug Fixes

* **ts/alias:** dispatch alias proxy instead of re-parsing ([d68b4a2](https://github.com/hop-top/poly-kit/commit/d68b4a2df72d470f3677b5f59321500e9d38e703))
* **ts/completion:** quote descriptions in zsh and fish scripts ([b03937c](https://github.com/hop-top/poly-kit/commit/b03937c9cd8fc9ed48baa6383d7f77deca89bc1e))
* **ts/toolspec:** forward spec --version past the program version flag ([afbf3a8](https://github.com/hop-top/poly-kit/commit/afbf3a89001d432dbbbf6a971051a5c6ea625e2f))
* **ts:** publish completion-values and compliance subpaths ([21d4370](https://github.com/hop-top/poly-kit/commit/21d4370791890f580574d4173923f77ae5ea8403))

Full diff: [kit-ts/v0.5.0-alpha.6...kit-ts/v0.5.0-alpha.7](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.5.0-alpha.6...kit-ts/v0.5.0-alpha.7)

## [0.5.0-alpha.6](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.5.0-alpha.5...kit-ts/v0.5.0-alpha.6) (2026-10-29)


### Features

* **ts:** publish log, completion and toolspec subpaths ([a349521](https://github.com/hop-top/poly-kit/commit/a3495219b223e25703df3da3f030813345d49525))

## [0.5.0-alpha.5](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.5.0-alpha.4...kit-ts/v0.5.0-alpha.5) (2026-10-29)

The hop-top team is happy to announce Kit's TS SDK 0.5.0-alpha.5. This release includes maintenance release with bug fixes.


### Bug Fixes

* **ts/cli:** render ARGUMENTS section in help ([ea98594](https://github.com/hop-top/poly-kit/commit/ea98594023b5082e965e476edf2148e68130d63d))
* **ts/cli:** route help &lt;command&gt; to command help ([6ce7c4c](https://github.com/hop-top/poly-kit/commit/6ce7c4c0f55a4b13359e4fa72c83414796664353))

Full diff: [kit-ts/v0.5.0-alpha.4...kit-ts/v0.5.0-alpha.5](https://github.com/hop-top/poly-kit/compare/kit-ts/v0.5.0-alpha.4...kit-ts/v0.5.0-alpha.5)

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
