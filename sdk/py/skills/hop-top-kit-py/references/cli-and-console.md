# CLI and console

Open when building commands, rendering output, shaping errors, or adding
aliases, completion, prompts or TUI widgets in this port. Reference pages:
[Python SDK reference](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/py-sdk.md) and
[Python CLI API reference](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/py-api-reference.md).

## Decisions

- One command, one payload: `dispatch(ctx, rows, columns=[ColumnSpec(...)])`
  from `hop_top_kit.output.dispatch`. A one-off render with no flags:
  `render(writer, "json", value)` from `hop_top_kit.output`; it cannot take
  `columns` or `--cols`.
- `ColumnSpec(header=, key=, priority=)`: `header` must equal `key` or it
  raises `ValueError`. Zero rows print nothing, not a header.
- Formats: `json`, `yaml`, `table`, `csv`, `text`, plus `--template` (Jinja2,
  exclusive with `--cols`). Resolution: `--format`, else the `-o` file
  extension, else table. A new format: implement `Formatter`, register it
  with `default_registry`.
- Failing under `--format json|yaml`: raise or render a `CLIError` built with
  `not_found_error`, `usage_error` or `wrap_error` from
  `hop_top_kit.output.error`; use its `EXIT_*` constants, never literals.
- Built-in root flags from `create_app`: `-v`, `-V`, `--format`, `--quiet`,
  `--no-color`, `--no-hints`, `--offline`, `--stream`. Drop some with
  `Disable(...)`, add persistent ones with `GlobalFlag`. `--confirm`,
  `--max-ops`, `--policy` and `--dry-run` are reserved for the delegation
  gate and raise `ValueError` as a `GlobalFlag`.
- Help groups: `GroupConfig`, `HelpConfig`, `set_command_group` in
  `hop_top_kit.cli`.
- Aliases: `hop_top_kit.alias` (`Expander`, seeded < global < project).
- Widgets: `hop_top_kit.tui` (`status`, `badge`, `pills`, `spinner`,
  `progress`, `confirm`, `select`, `anim`), themed from `Theme`. No list or
  dialog widget.

## Check coverage first

Cell `unverified` in [coverage](coverage.md), so test behaviour before
promising Go parity: `hop_top_kit.log`, `hop_top_kit.completion`,
`hop_top_kit.setflag` with `textflag` and `flagregister`,
`hop_top_kit.errcorrect`, `hop_top_kit.uri`, `hop_top_kit.wizard`,
`hop_top_kit.progress`. Cell `partial`: `hop_top_kit.tui`.

Markdown rendering, config path commands, fuzzy matching and process status
have no module here: [other ports](other-ports.md).
