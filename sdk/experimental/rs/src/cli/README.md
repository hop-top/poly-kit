# cli

The kit CLI root (feature `cli`): the parts of Go `console/cli` a kit-rs CLI author needs: persistent root flags, `-v/--version`, help groups with `--help-all`, root aliases, flag-error surfaces, opt-in autocorrect, and shell completion. An author defines the tree with the kit-owned `CommandSpec` builder (`spec.rs`) and never names clap: clap is the parsing provider, confined to `adapter.rs` and the parse seams in `run.rs`, `usage.rs` and the completion walkers. The kit-owned command model (`model.rs`) is what other surfaces project.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`spec.rs`](spec.rs) | `CommandSpec`, `FlagSpec`, `ArgSpec`, `ValueSpec`: the builder (name, about, flags, args, groups, annotations, side-effect tier, handler) | you define a command |
| [`adapter.rs`](adapter.rs) | `CommandSpec` to clap: the only place the provider's builder is called | you swap or debug the parse provider |
| [`config.rs`](config.rs) | `Config`, `Disable`, `GlobalFlag`, `GroupConfig`: what `Root::with_config` builds from | you start a kit CLI |
| [`run.rs`](run.rs) | `Root::execute` / `run`: alias expansion, help-group flags, USAGE envelopes, autocorrect, handler dispatch; `Invocation`, `Globals` | a command must run |
| [`usage.rs`](usage.rs) | flag-error surfaces (unknown flag, missing value, unknown command, suggestions) and autocorrect | you change an error text |
| [`model.rs`](model.rs) | `Root::model()`: the kit-owned `CommandInfo` / `FlagInfo` tree a surface projects | a surface must read the tree without clap |
| [`root.rs`](root.rs) | `Root`: wraps the root `CommandSpec`; adds the hidden `completion` command unless a command or alias owns the name (or `disable_completion_command`); the `__complete` entry point | your CLI must offer shell completion |
| [`alias.rs`](alias.rs) | root aliases with Go's texts; a deep alias is Go's flagless shim; `alias_with` registers a proxy shaped by `AliasOptions`; `validate_alias_name`, `detect_cycles` | a short name must reach a nested command |
| [`enums.rs`](enums.rs) | flag value enums: completion values and the `(one of: …)` help suffix | a flag takes a closed set of values |
| [`completion_spec/`](completion_spec/README.md) | `completion fig` / `completion usage`: Fig and Usage KDL specs of the tree, `Spec::from_root`, `render_fig`, `render_usage` | a terminal reads a completion spec, not a shell script |
| [`completion/`](completion/README.md) | completer API (`static_values`, `func`, `prefixed`, `config_keys`, `file`, `dir`, `Registry`), `bind_flag` / `bind_args`, the scripts | a flag or argument needs dynamic values |

## Conventions

- Go `console/cli` is canonical; behaviour is pinned by [`contracts/cli-root-v1`](../../../../../contracts/cli-root-v1/README.md) (root, flag errors, autocorrect), [`contracts/completion-v1`](../../../../../contracts/completion-v1/README.md); aliases by [`contracts/alias-v1`](../../../../../contracts/alias-v1/README.md).
- Ported: the root flags, version, `--help-all` / `--help-<group>` (visibility, not section titles), aliases, flag errors, autocorrect `read` mode. Not ported: themed help layout, the `help <path>` subcommand, the alias store, `--confirm` / `--max-ops` / `--policy`, `--api-version`, `prompt`-mode autocorrect, the `api` / `auth` / `audit` / `config` command groups.
- `-C/--chdir` and `-c/--config` are parsed and carried in `Globals`; `execute` is re-entrant and never changes the process directory.
- The `management` group is `hide(true)` on the provider's command: clap has no command groups.
- `Root::command()` and the help builders are crate-private. Help text is `Root::render_help` / `render_help_all` / `render_help_group` / `render_command_help`; the tree is `Root::model()`.
