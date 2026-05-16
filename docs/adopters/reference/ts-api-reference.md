# TypeScript CLI API Reference

> Reference for `@hop-top/kit/cli`. Mirrors the
> [Go reference](cli-api-reference.md) — same contract, native
> Commander types.

## Who this is for

TypeScript authors building a tool with kit's CLI factory. If you
are adopting kit for the first time, start with the
[top-level README](../../../README.md#ts-tools).

## Before you begin

```bash
pnpm add @hop-top/kit
```

```ts
import { createCLI } from '@hop-top/kit/cli';
```

## Recommended path

```ts
const program = createCLI({
  name: 'mytool',
  version: '1.0.0',
  description: 'does things',
});

program.command('list').action(() => { /* ... */ });

program.parse();
```

## Verify the result

```bash
mytool --help          # styled help
mytool --version       # "mytool 1.0.0"
mytool --help-all      # also shows hidden management groups
```

---

## Reference

### CLIConfig

```ts
interface CLIConfig {
  name: string;        // binary name (e.g. "mytool")
  version: string;     // semver (e.g. "1.2.3")
  description: string; // one-line help description
  groups?: GroupConfig[];
}
```

### createCLI

```ts
function createCLI(cfg: CLIConfig): Command
```

Returns a Commander `Command` pre-configured to the hop-top CLI
contract:

- No help/completion subcommands; `-h`/`--help` flag only.
- `-v, --version` prints `<name> <version>` and exits.
- Global options: `--format`, `--quiet`, `--no-color`.
- `showHelpAfterError` enabled.

### Command groups

#### GroupConfig

```ts
interface GroupConfig {
  id: string;      // unique identifier (e.g. "management")
  title: string;   // display title (e.g. "MANAGEMENT COMMANDS")
  hidden: boolean; // true = excluded from default --help
}
```

Default groups when none specified:

| id | title | hidden |
|----|-------|--------|
| `commands` | COMMANDS | false |
| `management` | MANAGEMENT COMMANDS | true |

#### setCommandGroup

```ts
function setCommandGroup(cmd: Command, groupId: string): void
```

Assigns a subcommand to a named group. Commands without assignment
default to `commands`.

```ts
const program = createCLI({ name: 'mytool', version: '1.0.0', description: '...' });
const configCmd = program.command('config').description('Manage configuration');
setCommandGroup(configCmd, 'management');
```

#### `--help-all`

Root-level boolean option. When passed, the help formatter includes
commands from hidden groups.

```
$ mytool --help          # shows COMMANDS only
$ mytool --help-all      # shows COMMANDS + MANAGEMENT
```

### Output

Package: `@hop-top/kit/output`

Provides `renderTable`, `renderJSON`, `renderYAML` for consistent
output formatting across tools.

## Related pages

- [`cli-api-reference.md`](cli-api-reference.md) — Go equivalent
- [`py-api-reference.md`](py-api-reference.md) — Python equivalent
- [`cli-parity-guide.md`](../guides/cli-parity-guide.md) — required flags + parity contract
