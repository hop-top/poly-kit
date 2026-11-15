# MessageFormat 2 test suite (vendored)

Unmodified copy of the data-driven test suite of the Unicode MessageFormat Working Group.

| | |
|---|---|
| Upstream | https://github.com/unicode-org/message-format-wg, directory `test/` |
| Tag | `LDML48.2` (spec release matching CLDR 48.2) |
| Commit | `7f142fb4f1f5ea6ab1eb34ce2b87e918ca9fd331` |
| License | Unicode License v3, text in `LICENSE` (the upstream repository LICENSE). The notice must stay with every copy of these files. Upstream relicensed the tests from the messageformat project under it. |
| Files | `tests/`, `schemas/v0/tests.schema.json`: identical to upstream paths under `test/`. `UPSTREAM-README.txt` is upstream's `test/README.md` (test functions, error names, tags), renamed so the page does not carry upstream-relative links. |
| Integrity | `source.json` holds the tag, commit and a sha256 per file; `make test-parity-i18n` fails when a file differs. |

## Mandatory files

A port MUST pass these, replaying every case from the file:

| File | Notes |
|------|-------|
| `tests/syntax.json`, `tests/syntax-errors.json`, `tests/data-model-errors.json` | parsing and static validation; a syntax or data-model error is the case's `expErrors` |
| `tests/fallback.json`, `tests/pattern-selection.json` | need the test-only functions `:test:function`, `:test:select`, `:test:format` (see `UPSTREAM-README.txt`); the harness registers them, the shipped port does not |
| `tests/bidi.json` | both `bidiIsolation` values; production default is the Default Bidi Strategy |
| `tests/u-options.json` | cases tagged `u:id` or `u:dir` are optional |
| `tests/functions/string.json`, `number.json`, `integer.json`, `offset.json` | required functions (see SPEC) |

## Optional files and tags

| File or tag | Rule |
|-------------|------|
| `tests/functions/currency.json`, `percent.json`; tags `:currency`, `:percent` | optional functions; a port that accepts the function passes the cases, otherwise skips them |
| `tests/functions/date.json`, `time.json`, `datetime.json` | draft in LDML 48.2; same rule |
| tags `u:id`, `u:dir` | a port that implements the option passes the cases, otherwise skips them |

Run every case with its own `locale`, `params`, and `bidiIsolation` (file default under `defaultTestProperties`, case value wins).

## Cases without `exp`

`exp` is often omitted in `functions/` because built-in formatting is locale-data dependent. Then the case asserts only `expErrors`: the port MUST emit exactly the listed error types (as a set) and MUST NOT throw out of the format call; the produced text is not compared. When `exp` is present the port MUST match it, with the test's locale and bidi setting. An absent or empty `expErrors` means the message formats without error. `expParts` (formatting to parts) is checked only by a port that offers a parts API. The development flag `only` is ignored.

## Update procedure

1. Pick the new upstream tag (an `LDML<major>[.<minor>]` release tag) and check the Part 9 spec changes that tag brings.
2. Copy `test/tests` and `test/schemas` from that tag over `tests/` and `schemas/`; replace `LICENSE` and `UPSTREAM-README.txt` from the same tag. Do not edit vendored files.
3. Rewrite `source.json`: tag, commit (`git rev-parse` of the tag), sha256 of each file (`shasum -a 256`).
4. Update the pinned tag in `SPEC.md` and this README; update the mandatory lists if upstream added, renamed or re-tagged files.
5. Run `make test-parity-i18n`, then each port's suite; a port that cannot pass yet records the failing cases in its tracker before the contract change lands.
