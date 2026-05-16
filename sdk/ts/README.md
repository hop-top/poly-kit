# @hop-top/kit (TypeScript)

Shared CLI utilities for hop-top tools — CommonJS edition.

Mirrors the Go `kit` package surface: a Commander-based CLI factory,
output formatting with a registry/Formatter contract, theming, hint
plumbing, XDG paths, config files, an embedded SQLite store, upgrade
checks, LLM helpers, alias resolution, and a TUI toolkit.

## Install

```sh
pnpm add @hop-top/kit
```

Subpath imports follow the package's `exports` map (see
`package.json`): `@hop-top/kit/cli`, `@hop-top/kit/output`,
`@hop-top/kit/xdg`, `@hop-top/kit/uri`, …

## Output formatting

The `output` module ships a Formatter + Registry contract identical to
the Go `go/console/output` package, plus a Commander integration layer
that wires the standard flag suite onto your program.

### Built-in formatters

| Key     | Extensions     | Options                                                                           |
| ------- | -------------- | --------------------------------------------------------------------------------- |
| `json`  | `.json`        | `indent` (int, default 2)                                                         |
| `yaml`  | `.yaml`, `.yml`| `flow-level` (int, default -1)                                                    |
| `table` | (none)         | (none)                                                                            |
| `csv`   | `.csv`         | `delimiter` (string), `no-header` (bool), `quote-all` (bool), `crlf` (bool)       |
| `text`  | `.txt`         | `style` (enum kv\|lines\|paragraph), `separator` (string, kv only)                |

### Wiring into a Commander CLI

```ts
import { Command } from 'commander';
import { registerOutputFlags, dispatch } from '@hop-top/kit/output';

const program = new Command('mytool');
registerOutputFlags(program);

program.command('list').action(async () => {
  await dispatch(program, [{ id: '1', name: 'Alice' }]);
});

program.parseAsync();
```

`registerOutputFlags` adds the full suite — opt-out per flag via
`{ disable: { format?, formatOpt?, formatHelp?, cols?, template?, output? } }`.

### Flag surface

- `--format <fmt>` — pick a registered formatter (default `table`).
- `--format-opt <kv...>` — repeatable key=value pairs validated against
  the active formatter's option specs (e.g. `--format-opt delimiter=';'`).
  Bool keys may omit `=value`.
- `--format-help [fmt]` — without an argument, list every registered
  formatter; with an argument, print that formatter's options table.
  Short-circuits before render.
- `--cols`, `--columns <cols...>` — restrict output to the named
  columns. Accepts comma-separated or repeated flags. Honored by all
  five built-ins.
- `--template <tpl>` — eta template applied to results. Mutually
  exclusive with `--cols`. Template input shape: `{ items, cols, data }`.
  See [eta syntax](https://eta.js.org/docs/intro/syntax) — note the
  EJS-style `<%= %>` tags differ from Go's `{{ }}` text/template syntax.
- `-o`, `--output <path>` — write to a file instead of stdout. Empty
  string or `-` means stdout. Extension inference: when `--format` is
  default and the path's extension matches a registered formatter, that
  formatter is selected (e.g. `-o report.csv` → csv). Mismatch is a
  hard error (e.g. `--format json -o report.csv`).

### Programmatic Formatter API

```ts
import {
  defaultRegistry,
  Registry,
  type Formatter,
} from '@hop-top/kit/output';

// Custom formatter:
const htmlFormatter: Formatter = {
  key: 'html',
  extensions: ['.html'],
  options: [],
  render(out, data, _opts, cols) {
    // ... render HTML to `out`
  },
};

// Replace a built-in or add a new one:
defaultRegistry.override(htmlFormatter); // or .register() to fail-loud on dup

// Isolated registry for tests/multi-CLI binaries:
const r = new Registry();
r.register(htmlFormatter);
```

### Column metadata

TS lacks struct tags; pass an explicit `ColumnSpec[]` list to `dispatch`
when you want headers/keys to differ or want priority ordering for
table layout:

```ts
import type { ColumnSpec } from '@hop-top/kit/output';

const userCols: ColumnSpec[] = [
  { header: 'ID',    key: 'id',    priority: 9 },
  { header: 'Name',  key: 'name',  priority: 8 },
  { header: 'Notes', key: 'notes', priority: 2 },
];

await dispatch(cmd, users, { columns: userCols });
```

When no `columns` is passed, headers are derived from the first row's
own enumerable keys.

### Backward compatibility

The legacy `render(w, format, v)` signature stays. It is now a thin
shim over `defaultRegistry.lookup(format).render(...)`. Existing
callers do not need to migrate.

```ts
import { render, JSON_FORMAT } from '@hop-top/kit/output';
render(process.stdout, JSON_FORMAT, { ok: true });
```

## Other modules

- `@hop-top/kit/cli` — `createCLI(cfg)` builds a Commander root program
  with the hop-top contract: `--format`, `--quiet`, `--no-color`,
  `--no-hints`, themed help, version, hidden completion command.
- `@hop-top/kit/xdg` — XDG Base Directory paths.
- `@hop-top/kit/config` — config-file loading.
- `@hop-top/kit/sqlstore` — embedded SQLite key/value store.
- `@hop-top/kit/upgrade` — semver upgrade detection.
- `@hop-top/kit/llm` / `routellm` — LLM client + routing helpers.
- `@hop-top/kit/alias` — alias resolution + completion.
- `@hop-top/kit/uri` — thin facade over `@hop-top/uri` for URI parsing,
  action resolution, completions, registries, and OS handler metadata.
- `@hop-top/kit/tui` — TUI toolkit (parity, anim, prompts).

See package.json `exports` for the full list.

## URI facade

The URI module delegates to `@hop-top/uri`; it does not reimplement the URI
contract. It mirrors the kit Go URI command intent for SDK consumers:

```ts
import { parse, resolve, complete, handler, newRegistry } from '@hop-top/kit/uri';

const policy = {
  schemeNamespaceSegments: { tlc: 2 },
  actionRoutes: {
    'task.claim': {
      command: 'tlc',
      args: ['-C', '{namespace}', 'task', 'claim', '{id}'],
    },
  },
};

const parsed = parse('tlc://hop-top/kit/T-0001?cmd=task&verb=claim', policy);
const plan = resolve(parsed, policy); // command plan only; never executes

const registry = newRegistry(policy);
registry.register({ name: 'tlc', completer: async () => ['hop-top/kit/T-0001'] });
const suggestions = await complete(registry, 'tlc', 'tlc://T-');

const id = handler.id({
  vendor: 'hop-top',
  app: 'tlc',
  language: 'ts',
  scheme: 'tlc',
  appPath: '/usr/local/bin/tlc',
});
```

## License

MIT.
