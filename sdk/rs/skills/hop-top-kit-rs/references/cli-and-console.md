# CLI and console

Open when building the CLI root, its flags and help, completion, aliases or
progress output. Long form: [Rust SDK reference](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/rs-sdk.md#cli-root).

| Need | Feature | Entry point |
|------|---------|-------------|
| Root, persistent flags, help groups, completion, aliases | `cli` (adds `output`) | `cli::Root::with_config`, `CommandSpec`, `FlagSpec`, `ArgSpec` |
| `--format` table, json, yaml, csv, text, human | `output` | `Invocation::render`, `Formatter`, `CliError` |
| `-c file` and `-c key=value` layering | `cli` with `config` | `Invocation::layer_config` |
| Served-call gate: callers, permission rules, `max_ops` | `cli-permissions` | `cli::ServedGate` |
| Progress bar, spinner, status lines, yes/no prompt | `tui` (std only) | `tui` module |
| Cross-language status symbols, spinner frames | `output` | `parity` module |

Start from [the first snippet](../assets/snippets/cli-root/cli-root.rs).
The `-V`/`--quiet` logger, `--dry-run` and the `--force` guard live on the
same root; a shipped tool leaves `disable_validate` off and declares each
command's side-effect tier.

Gaps taken from the parity page:

- CLI factory: the page names no Rust gap (its note is PHP's missing
  global-flag layer); read the row's Evidence in [coverage](coverage.md).
- TUI components: progress, bar, spinner, status, confirm and app shell
  exist; no list or dialog component.

[Coverage](coverage.md) lists the rows with no Rust module, for example
the `config path` commands and autocorrect.
