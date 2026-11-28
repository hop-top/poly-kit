# CLI and console

Open when you are writing commands, choosing output formats or gating a
command tree. Rows for this area are in [coverage](coverage.md); check them
first.

## Pick the entry point

| You have | Use |
|----------|-----|
| a new Symfony Console command returning rows or a map | extend `KitCommand`, implement `handle()`, call `$this->render()` |
| a command that already extends another base class | `KitOutput::for($input, $output)->application($this->getApplication())->render($data)` |
| a tree that needs `--confirm`, `--policy`, `--max-ops` or `--dry-run` | `Root` with `CommandSpec` leaves annotated `kit/side-effect` |
| a `serve` verb | see [serving-and-transports](serving-and-transports.md) |

`render()` accepts a list of rows or one map. Pass `columns:` as a list of
`ColumnSpec::of(key, header)` to fix column order and validate `--cols`.

## Decisions

- Output formats are `table`, `json` and `yaml`. A format Go has and PHP
  lacks is a gap: see the Evidence in [coverage](coverage.md) and the
  [parity guide](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/guides/cli-parity-guide.md).
- Only the output flags and the delegation flags are wired. Verbosity,
  config-file loading and the rest of Go's global flags are not: say so
  rather than adding them by hand and calling it parity.
- Inside another framework's console (a Laravel app, for example): its
  commands extend Symfony Console's command class, so the second row above
  applies. That `Flags::register()` accepts the framework's own application
  object is not tested here; probe it before promising output flags there.
- URI parsing is a thin facade over the `hop-top/cite` library; its coverage
  cell says how far to trust it.

## Detail

Package READMEs ship with the install: `src/Cli/README.md`,
`src/Output/README.md`, `src/Output/Formatter/Builtin/README.md` (the
`--format-opt` keys), `src/Uri/README.md`, all under `vendor/hop-top/kit/`.
Long-form rules and worked examples:
[PHP SDK reference](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/reference/php-sdk.md).
