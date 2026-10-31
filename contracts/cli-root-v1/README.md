# cli-root-v1

## What it answers

What a kit CLI root does at the command line, independent of language: which persistent flags it registers and how they resolve, what `-v` / `--version` print, what default help hides and `--help-all` shows, how root aliases dispatch, what each flag error looks like (text, JSON and YAML envelopes), when opt-in autocorrect applies a fix, and what the root enforces before a command runs: `-C/--chdir`, `-c/--config` layering, the `--confirm` / `--max-ops` / `--policy` delegation gates, `--dry-run`, flag validators, `Root.Validate` and the signature validators, the alias store commands and `config path|paths`.

## Use it when

- you change the root's flags, aliases, flag-error texts or autocorrect in any SDK: add a vector first, then make every port pass it
- you port another piece of `go/console/cli` that shows at the command line

## Quick start

```sh
go test ./go/console/cli/ -run '^TestConformanceCLIRoot'
cargo test --features cli,config --test cli_root_conformance   # from sdk/experimental/rs
```

## Contract

Go is canonical. A runner failure means the implementation drifted, not that the vector should be edited.

`vectors.json` holds named `trees` and `cases`. A case builds its tree under a kit root, runs `args` with `env` set, and pins the exit code and stream contents. Every runnable leaf echoes `{path, flags, args, globals}` as one JSON line (keys sorted): `flags` are the leaf's own flags that were set, `globals` the resolved persistent flags as strings (`verbose` is a number). Help is pinned by what it lists (`stdout_contains`, `stdout_omits`), never byte for byte. `stderr_contains` entries are substrings of the text-format envelope; `stderr_json` keys are compared to the structured envelope.

Pinned: persistent flags before and after the command, `-VV` counting, `-v` and `--version`, `--` ending options, unknown / near / value-less / badly typed flags, unknown and near-miss commands, the value-name fallback to the type name (`--env <string>`), enum alternatives on a value-less flag, direct and deep aliases, and autocorrect (`read` applies on a `kit/side-effect: read` leaf only; `--autocorrect` beats `KIT_AUTOCORRECT`; off by default).

A tree may also carry `fixtures` (files, or directories with a trailing slash, created in a per-case temp dir that is the working directory; `{tmp}` in any string names it), `validate` (the pre-flight `Root.Validate`; a failure comes back unrendered, and the runner shows its message on stderr), `signature` (`reject`), `policy` (named policies resolve under `$XDG_CONFIG_HOME/<program>/policies/`, which the runner points into the temp dir), `chdir_resolver` (a `-C` target to a fixture directory), `project_marker`, `flag_validators` (reject listed values of a flag with a code and message), `alias_store` (a fixture file the `alias` and `aliases` commands are backed by) and `config_chain` (the chain `config path|paths` walks). A command may set `kind` (`pwd` prints the working directory's base name, `dryrun` whether kit applied `--dry-run`, `config` the `-c` paths and overrides), `annotations`, `dry_run: opted-out` and a `long` description. A case may set `prompt_answer`, the line the confirm prompt reads; absent, there is no terminal to ask on.

Also pinned: `-C` as a directory, through the resolver and refused; `-c` pairs, files, a project-marker directory and the refusals; every `--confirm` mode (`yes`, `auto`, `no`, `prompt` answered `y` and `n`, the unset default by terminal availability), the typed `--confirm-token`, a `--policy` refusal that `--confirm=yes` does not lift, `require_confirm`, an unknown or unparsable policy as USAGE, the shipped `kit-default` and its remedy, a leaf-local `--policy` that is not the delegation policy, `--max-ops` within budget; `--dry-run` on a write, a destructive (no confirmation), a read (no-op), an interactive and an opted-out leaf; a flag validator's refusal, acceptance and skipped default; each `Root.Validate` bucket named in the failure; each signature check; the alias commands' output; `config path` and `paths` in text and JSON.

Not pinned: help layout and section titles, the `--format` flag's format list, `prompt`-mode autocorrect, a value-less `--format json ghost` (Go renders that unknown-command refusal as text because the flag is read before the parse), the second line fang prints after a validator's envelope, the `--max-ops` refusal past the budget (one invocation runs one command; the engine's `RecordOp` is unit-tested in each port), the dry-run plan's timestamp, and the Go-only validation arms (`kit/idempotent`, `kit/cache-ttl`, `kit/output-schema`, the reserved `status`).

## Runners

| Port | Runner |
|------|--------|
| Go | `go/console/cli/cli_root_conformance_test.go` |
| Rust | `sdk/experimental/rs/tests/cli_root_conformance.rs` |
| TypeScript | `sdk/ts/src/cli.root.conformance.test.ts` (delegation + `infer` cases; the rest skipped with a stated reason) |
| Python | `sdk/py/tests/test_cli_root_conformance.py` (delegation + `infer` cases; the rest skipped with a stated reason) |

The TypeScript and Python ports have no `Root.Validate`; they refuse a malformed `kit/side-effect` at run time instead, so the case that runs a malformed leaf unvalidated is skipped there. PHP has no CLI root, so no runner; its MCP gate shares the destructive-name inference.

## See also

- [`completion-v1`](../completion-v1/README.md): the `rs --` request reads the same root flags
- [`sdk/experimental/rs/src/cli/`](../../sdk/experimental/rs/src/cli/README.md)
