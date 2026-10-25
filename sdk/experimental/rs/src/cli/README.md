# cli

The kit CLI surface on clap (feature `cli`): today, the parts of Go `console/cli` that shell completion reads.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`root.rs`](root.rs) | `Root`: wraps a finished clap tree; adds the hidden `completion` command, root aliases, flag value enums, the `__complete` entry point | your CLI must offer shell completion |
| [`completion/`](completion/README.md) | completer API (`static_values`, `func`, `prefixed`, `config_keys`, `file`, `dir`, `Registry`), `bind_flag` / `bind_args`, the scripts | a flag or argument needs dynamic values |

## Conventions

- Go `console/cli` is canonical; behaviour is pinned by [`contracts/completion-v1`](../../../../../contracts/completion-v1/README.md).
- Only what completion needs is ported: no help layout, no `--help-all` flag (use `Root::help_all_command`), no alias store, no flag-enum help or error consumers beyond the help suffix.
- The `management` group is clap's `hide(true)`: clap has no command groups.
