# CLI and console

Open when wiring a command tree, output flags, completion, aliases, styled terminal output,
logging, layered config or XDG paths. Status per capability: [coverage](coverage.md). Depth:
[TypeScript API reference](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/reference/ts-api-reference.md).

## Modules

| Import | Use for | Cue |
|--------|---------|-----|
| `@hop-top/kit/cli` | `createCLI`, command groups, delegation gates | `setCommandGroup`, `verboseCount`, `isQuiet`, `isDryRun`, `renderPlan`, `buildTheme` |
| `@hop-top/kit/output` | five formatters behind `--format` | `registerOutputFlags`, `dispatch`, `newRegistry`; `render` is the legacy shim |
| `@hop-top/kit/completion` | `completion bash\|zsh\|fish\|powershell` | `completionCommand(program)`; `bindFlag`, `bindArgs` attach completers |
| `@hop-top/kit/completion-values` | completer building blocks | used with `bindFlag` and `bindArgs` |
| `@hop-top/kit/alias` | user aliases for subcommands | `AliasStore`, `Expander`, `bridgeAliases` |
| `@hop-top/kit/tui` | status, badge, spinner, progress, prompts | `status(theme, text, level)`; theme from `buildTheme()` |

Matrix cell `unverified` (module exists, no Go pin): `@hop-top/kit/log` (`createLogger`),
`@hop-top/kit/progress` (`ProgressReporter`), `@hop-top/kit/errcorrect`
(`createCorrectedError`), `@hop-top/kit/config`, `@hop-top/kit/xdg`, `@hop-top/kit/sqlstore`.
Check each in coverage, tell the user, and test the behaviour you rely on.

URI scheme handlers, the wizard and flag helpers also sit under `unverified`, but their source
files have no key in the package `exports` map when this was written: you cannot import them.
Check the map.

## Output flags

- `createCLI` registers the suite; `--format` default is `table`. `-o report.csv` infers the
  formatter from the extension; `--format json -o report.csv` is a hard error.
- `--template` takes eta syntax (`<%= %>`), not Go's `{{ }}`; it excludes `--cols`.
- Pass `columns` to `dispatch` for a fixed order; without it, rows print in key order.
- A formatter option goes `--format-opt key=value`; `--format-help` lists them.

## Layered config and paths

```ts
import { load } from '@hop-top/kit/config';
import { configDir } from '@hop-top/kit/xdg';
import { join } from 'node:path';

const cfg = load<{ host?: string }>({ host: 'localhost' }, {
  userConfigPath: join(configDir('mytool'), 'config.yaml'),
  projectConfigPath: '.mytool.yaml',
  envOverride: (c) => { if (process.env.MYTOOL_HOST) c.host = process.env.MYTOOL_HOST; },
});
```

- Order, lowest first: system, user, project, `envOverride`. A missing file is skipped.
- `load` fills and returns `dst`. Objects merge deeply; arrays and scalars replace. Go differs
  on map entries and kind mismatches: do not share one config file contract with Go unchecked.
- `configDir` is `XDG_CONFIG_HOME` when set; otherwise macOS `~/Library/Preferences/<tool>`,
  not `~/.config`. Pin `XDG_CONFIG_HOME` in tests.
- Merge options (`nullPolicy`, `arrayPolicy`, `maxDepth`) and `mergeLayers` for parsed objects:
  see the API reference, "Layer merge rules".

## Traps

- `program.command('x').action(async ...)` needs `await program.parseAsync()`.
- Do not name your own leaf flag `--policy`, `--confirm`, `--max-ops` or `--dry-run` for another
  meaning: the root owns them (a leaf's own flag of that name is the leaf's).
- Not in this port (coverage lists them): markdown rendering, config path commands, fuzzy
  matching, process status. Use Go or the sidecar.
