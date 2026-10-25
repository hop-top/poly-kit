# config-v1

## What it answers

What `kit/core/config` must compute, independent of language: CLI scalar typing, YAML scalar typing on read, `-c key=value` overrides, the precedence ladder, layered load merge rules, and what `set`/`unset` leave in a file. Wrong place for paths, XDG and reload; those depend on the filesystem and process and are covered by each port's tests.

## Use it when

- you port or change config parsing, merging or writing in any SDK: run both runners
- you change config behaviour in Go: add a vector first, then make every port pass it

## Quick start

```sh
make test-parity-config
# or separately:
go test ./go/core/config/ -run '^TestConformance'
cargo test --features config --test config_conformance   # from sdk/experimental/rs
```

## Contract

Go is canonical. The vectors are the source of truth: a runner failure means the implementation drifted, not that the vector should be edited. Every file carries `version: "v1"` and a `description` stating the rules it pins.

| File | Cases | Pins |
|------|-------|------|
| `parse_scalar.json` | `{input, want}` | `ParseScalar`: narrow, never lossy typing of CLI text |
| `resolve.json` | `{yaml, want}` | `Get` typing: only int/float/bool/null tags convert, other tags yield source text |
| `overrides.json` | `{pairs, ok, want \| error}` | `ParseOverrides`: dotted nesting, YAML values with raw fallback, exact errors |
| `precedence.json` | `env_names[]`, `{key, layers, want}` | `ResolveString` hit order, env names, details |
| `load.json` | `{start, files, overrides, ok, want \| error_contains}` | `Load` merge into a fixed struct shape |
| `edit.json` | `{original, op, key, value, ok, want, keeps \| error}` | `Set`/`SetValue`/`Unset`: resulting values and kept comments |

Values are `{kind, repr}` (kind `int`, `float`, `bool`, `null`, `string`; repr is Go's `%v`), `{seq: [...]}` or `{map: {...}}`. `<path>` in an error stands for the failing file. Edits compare values and kept substrings, not bytes: Go re-emits the document, ports may splice.

Runners are strict: unknown fields, a `version` other than `v1` and a corpus file no runner consumes all fail the run. Not in the corpus, because the ports differ by design: timestamps (Go decodes `time.Time`, Rust keeps text) and Go type names inside decode errors.

## Runners

| Port | Runner |
|------|--------|
| Go | `go/core/config/conformance_test.go` (`TestConformance*`) |
| Rust | `sdk/experimental/rs/tests/config_conformance.rs` (feature `config`) |
