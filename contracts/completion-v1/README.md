# completion-v1

## What it answers

What shell completion in a kit CLI must produce, independent of language: what each completer in `kit/console/cli/completion` returns for a prefix, what the hidden `__complete` entry point prints for a command line, and the `completion <shell>` scripts byte for byte. Wrong place for how a shell renders the candidates; that belongs to the scripts, which are pinned whole.

## Use it when

- you port or change a completer (`Static`, `StaticValues`, `Func`, `Prefixed`, `ConfigKeys`, `File`, `Dir`) or the shell bridge in any SDK: run both runners
- you change alias completion, enum-flag completion or the `completion` command in Go: add a vector first, then make every port pass it

## Quick start

```sh
make test-parity-completion
# or separately:
go test ./go/console/cli/completion/ -run '^TestConformanceCompletion'
cargo test --features cli,config --test cli_completion_conformance   # from sdk/experimental/rs
```

## Contract

Go is canonical. The vectors are the source of truth: a runner failure means the implementation drifted, not that the vector should be edited. Each file carries `version: "v1"` and a `description` stating the rules it pins.

| File | Pins |
|------|------|
| `completers.json` | per completer spec and prefix: the items `Complete` returns (or that it fails), and the bridge output, values joined to descriptions by a tab plus the directive integer |
| `requests.json` | named command trees with aliases (including one named `completion`), enum flags and bound completers; per `__complete` / `__completeNoDesc` command line, the exact stdout including the `:<directive>` line, and optionally a stderr substring (the directive's name) |
| `scripts.json` + `scripts/` | `completion bash\|zsh\|fish\|powershell [--no-descriptions]` output for a program name, byte for byte |

Directives are cobra's bit set: 0 default, 1 error, 2 no space, 4 no file completion, 8 filter by extension, 16 directories only, 32 keep order.

Prefix matching in the completers is case-insensitive; command, alias and flag-name matching in `__complete` is case-sensitive, as in cobra. Alias completion order follows Go map iteration, so those cases are `unordered`.

After a cobra bump, `make completion-regen` rewrites the script goldens and the Rust and TS templates; see [`scripts/`](scripts/README.md).

Runners are strict: unknown fields, a `version` other than `v1`, a corpus file no runner consumes, a tree no case uses and a golden no case claims all fail the run.

## Runners

| Port | Runner |
|------|--------|
| Go | `go/console/cli/completion/completion_conformance_test.go` (`TestConformanceCompletion*`); template derivation in `completion_templates_test.go` |
| Rust | `sdk/experimental/rs/tests/cli_completion_conformance.rs` |

## See also

- [`go/console/cli/completion/`](../../go/console/cli/completion/README.md)
- [`sdk/experimental/rs/src/cli/`](../../sdk/experimental/rs/src/cli/README.md)
