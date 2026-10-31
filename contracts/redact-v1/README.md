# redact-v1

## What it answers

What `kit/core/redact` must produce from text, independent of language: which secret spans each rule finds, how overlapping spans merge, and the exact redacted output. Wrong place for observers, stats and the `Custom` strategy; those are per-runtime and have no vectors.

## Use it when

- you port or change the redactor in any SDK: run both runners
- you change rule semantics in Go: add a vector first, then make every port pass it
- you refresh a vendored corpus: regenerate `rules.json` and review its diff

## Quick start

```sh
make test-parity-redact
# or separately:
go test ./go/core/redact/ -run '^TestConformanceRedact'
cargo test --features redact --test redact_conformance   # from sdk/rs
# after a corpus change:
go test ./go/core/redact/ -run TestConformanceRedactRules -update
```

## Contract

Go is canonical. The vectors are the source of truth: a runner failure means the implementation drifted, not that the vector should be edited.

| File | Pins |
|------|------|
| `rules.json` | the default policy as data (`redact.EmbeddedPolicy()`): gitleaks v8.30.1 rules with the kit patches, then the Presidio pack, then global allowlists. Every rule carries `distinctive`, which each port must compute identically. The Rust crate embeds a byte-identical copy (`src/redact/rules.json`). |
| `vectors.json` | `{name, rules?, allowlists?, allowlist_exact?, strategy, input, pad?, findings?, spans, output}`; no `rules` means `rules.json` |

Semantics the vectors pin:

- Single pass. Every rule matches the original input; matches are trimmed of leading and trailing `\n`; the secret is the `secret_group` span (0, or a group that did not participate, means the whole match). Overlapping and adjacent spans merge: earliest start wins, then the longest, then the rule listed first. Replacements are never scanned.
- Distinctive: every match starts with a literal of 3+ characters, walked on the parsed pattern: literals count their characters, a class of at most 4 characters counts as one, an alternation adds its shortest branch and ends the prefix unless every branch is wholly literal, a repeat with minimum 1 adds its operand's prefix and ends it, empty-width assertions are skipped. Inline rules leave the field out; it is computed.
- Keywords: a rule runs only when the input lower-cased per rune with Go's simple mapping (U+0130 becomes `i`, the Kelvin sign `k`) contains one.
- `findings` keep the keyword gate and entropy floor for every rule; `spans` and `output` skip both for distinctive rules.
- Entropy: Shannon entropy over runes, divided by the byte length, summed in ascending rune order; a secret at or below the floor is dropped.
- Two allowlist modes. `findings` is detection (`Scan`): gitleaks semantics, each regex tested on its own and needing a non-empty match, stopwords as case-insensitive substrings of the secret. `spans` and `output` are redaction: fail closed, stopwords never exempt, a regex exempts only when it matches the whole target (`secret`, `match` or `line`). AND lists need every populated check; paths and commits never hold.
- `allowlist_exact` exempts a secret or whole match equal to an entry.
- Strategies: `mask` writes `***REDACTED***`, `tag` the rule's `replacement` or `<id>`, `hash` `sha256:` plus the first 4 bytes of the region's SHA-256 in hex.
- Offsets are UTF-8 byte offsets. `pad` wraps input and output in `text` repeated `times` times; offsets exclude it.
- Regex syntax is Go RE2: `\d`, `\s`, `\w` and `\b` are ASCII, `\s` excludes `\v`, `\Q…\E` quotes. The `re2-*` vectors fail a port that compiles patterns with Unicode Perl classes.

Runners are strict: unknown fields, a `version` other than `v1` and a corpus file no runner consumes fail the run. Secrets in `vectors.json` escape one character (`h`) so repository scanners stay quiet; the decoded strings are the fixtures.

## Runners

| Port | Runner |
|------|--------|
| Go | `go/core/redact/conformance_test.go` (`TestConformanceRedact*`) |
| Rust | `sdk/rs/tests/redact_conformance.rs` (feature `redact`) |

## See also

- [`go/core/redact`](../../go/core/redact/README.md), [`sdk/rs/src/redact`](../../sdk/rs/src/redact/README.md)
