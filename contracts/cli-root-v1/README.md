# cli-root-v1

## What it answers

What a kit CLI root does at the command line, independent of language: which persistent flags it registers and how they resolve, what `-v` / `--version` print, what default help hides and `--help-all` shows, how root aliases dispatch, what each flag error looks like (text, JSON and YAML envelopes), and when opt-in autocorrect applies a fix.

## Use it when

- you change the root's flags, aliases, flag-error texts or autocorrect in any SDK: add a vector first, then make every port pass it
- you port another piece of `go/console/cli` that shows at the command line

## Quick start

```sh
go test ./go/console/cli/ -run '^TestConformanceCLIRoot'
cargo test --features cli --test cli_root_conformance   # from sdk/experimental/rs
```

## Contract

Go is canonical. A runner failure means the implementation drifted, not that the vector should be edited.

`vectors.json` holds named `trees` and `cases`. A case builds its tree under a kit root, runs `args` with `env` set, and pins the exit code and stream contents. Every runnable leaf echoes `{path, flags, args, globals}` as one JSON line (keys sorted): `flags` are the leaf's own flags that were set, `globals` the resolved persistent flags as strings (`verbose` is a number). Help is pinned by what it lists (`stdout_contains`, `stdout_omits`), never byte for byte. `stderr_contains` entries are substrings of the text-format envelope; `stderr_json` keys are compared to the structured envelope.

Pinned: persistent flags before and after the command, `-VV` counting, `-v` and `--version`, `--` ending options, unknown / near / value-less / badly typed flags, unknown and near-miss commands, the value-name fallback to the type name (`--env <string>`), enum alternatives on a value-less flag, direct and deep aliases, and autocorrect (`read` applies on a `kit/side-effect: read` leaf only; `--autocorrect` beats `KIT_AUTOCORRECT`; off by default).

Not pinned: help layout and section titles, the `--format` flag's format list, `prompt`-mode autocorrect, and a value-less `--format json ghost` (Go renders that unknown-command refusal as text because the flag is read before the parse).

## Runners

| Port | Runner |
|------|--------|
| Go | `go/console/cli/cli_root_conformance_test.go` |
| Rust | `sdk/experimental/rs/tests/cli_root_conformance.rs` |

## See also

- [`completion-v1`](../completion-v1/README.md): the `rs --` request reads the same root flags
- [`sdk/experimental/rs/src/cli/`](../../sdk/experimental/rs/src/cli/README.md)
