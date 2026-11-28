# CLI and console

Open when: building commands and their output, or choosing a console
package (completion, aliases, prompts, TUI, markdown, progress).

## Decisions

| Need | Use |
|------|-----|
| root command, global flags, help, version | `cli.New` in `hop.top/kit/go/console/cli` |
| rows as table, JSON, YAML or CSV | `output.Dispatch(cmd, root.Viper, data)`; `output.Render` only when you hold a writer and a format |
| a failure with an exit code | `output.NotFoundError(msg)` or `output.WrapError(err, code, exit)`; errors render as the envelope for the chosen `--format` |
| a closed set of flag values | `root.WithFlagEnum`: errors, help and completion read one source |
| reject a bad flag value | `root.WithFlagValidator`, installed by `WrapRunE` (`Execute` calls it) |
| preview a write | `cli.RenderPlan` under `--dry-run`, or `cli.OptOutDryRun` with a rationale |
| dynamic shell completion | `go/console/cli/completion`; Fig or Usage spec: `go/console/cli/completionspec` |
| git-style aliases | `go/console/alias` |
| config path subcommands | `go/console/cli/config` |
| interactive prompts, forms | `go/console/wizard` (headless and line-based modes exist: use them off a terminal) |
| themed terminal UI | `go/console/tui` |
| markdown on the terminal | `go/console/markdown` |
| per-phase progress | `go/console/progress`: human lines or JSON, chosen by kit |
| fuzzy name matching, `<tool> ps` | `go/console/hay`, `go/console/ps` |
| your own `yourtool://` scheme | `go/console/uri` |

## Traps

- `cli.New` returns a `*cli.Root`; the cobra root is `root.Cmd`, the
  viper is `root.Viper`. Add commands to `root.Cmd`.
- `Execute` runs a validator over the tree before anything else (side
  effect, reserved verbs, top-level verb shape). Tests that build a bare
  tree set `DisableValidate`; products do not.
- `--format` defaults to `table`. Hints are suppressed for JSON and YAML,
  non-terminals and `--quiet`; do not print your own hint lines.
- A leaf named like `delete`, `remove`, `rm`, `destroy`, `purge` or `drop`
  is gated as destructive (needs `--confirm`) unless it declares its own
  `kit/side-effect`.
- Streaming commands must not take `-o`:
  `output.RegisterFlagsWith(cmd, v, output.DisableOutputFlag())`.
- Autocorrect only suggests unless `cli.autocorrect` is set.

## Pages

- [CLI API](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/cli-api-reference.md) and
  [getting started](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/getting-started-cli.md)
- [output](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/output.md),
  [flag enums](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/flag-enums.md),
  [help rendering](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/help-rendering.md)
- [command annotations](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/declare-and-read-command-annotations.md),
  [autocorrect](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/enable-flag-autocorrect.md),
  [progress](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/report-command-progress.md)
- [completion](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/completion-api.md),
  [aliases](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/alias-api.md),
  [wizard](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/wizard-api.md),
  [TUI gallery](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/tui-component-gallery.md),
  [markdown](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/render-markdown-in-the-terminal.md)
- [CLI parity guide](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/cli-parity-guide.md): the contract every port shares
