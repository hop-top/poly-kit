# cli

The kit CLI surface on clap (feature `cli`): today, the parts of Go `console/cli` that shell completion reads.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`root.rs`](root.rs) | `Root`: wraps a finished clap tree; adds the hidden `completion` command unless a command or alias owns the name (or `disable_completion_command`); the `__complete` entry point | your CLI must offer shell completion |
| [`alias.rs`](alias.rs) | root aliases with Go's texts; a deep alias is Go's flagless shim | a short name must reach a nested command |
| [`enums.rs`](enums.rs) | flag value enums: completion values and the `(one of: …)` help suffix | a flag takes a closed set of values |
| [`completion_spec/`](completion_spec/README.md) | `completion fig` / `completion usage`: Fig and Usage KDL specs of the tree, `Spec::from_root`, `render_fig`, `render_usage` | a terminal reads a completion spec, not a shell script |
| [`completion/`](completion/README.md) | completer API (`static_values`, `func`, `prefixed`, `config_keys`, `file`, `dir`, `Registry`), `bind_flag` / `bind_args`, the scripts | a flag or argument needs dynamic values |

## Conventions

- Go `console/cli` is canonical; behaviour is pinned by [`contracts/completion-v1`](../../../../../contracts/completion-v1/README.md).
- Only what completion needs is ported: no help layout, no `help <path>` subcommand, no `--help-all` flag (use `Root::help_all_command`), no alias store, no flag-enum help or error consumers beyond the help suffix.
- The `management` group is clap's `hide(true)`: clap has no command groups.
