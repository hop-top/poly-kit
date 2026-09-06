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
const { program } = createCLI({
  name: 'mytool',
  version: '1.0.0',
  description: 'does things',
});

program.command('list').action(() => { /* ... */ });

program.parse();
```

`createCLI` returns `{ program, theme }`, not the Commander command
itself. Destructure `program`, or reach it as `result.program`.

## Verify the result

```bash
mytool --help          # styled help
mytool --version       # "mytool v1.0.0"
mytool --help-all      # also shows hidden management groups
```

The version line always carries a `v` prefix. Pass `version` without
one; kit adds it.

---

## Reference

### CLIConfig

```ts
interface CLIConfig {
  name: string;        // binary name (e.g. "mytool")
  version: string;     // semver, no leading "v" (e.g. "1.2.3")
  description: string; // one-line help description
  accent?: string;     // hex theme accent (e.g. "#FF0000"); default Neon

  // Opt out of built-in global flags. Omit to keep all of them.
  disable?: {
    format?: boolean;
    quiet?: boolean;
    noColor?: boolean;
    hints?: boolean;
    offline?: boolean;
  };

  // Command groups for partitioned help.
  groups?: Array<{
    id: string;
    title: string;
    hidden?: boolean;
  }>;

  // Extra tool-specific persistent flags on the root command.
  globals?: Array<{
    name: string;     // long name without "--"
    short?: string;   // single char
    usage: string;
    default?: string;
  }>;

  // Root --help layout overrides; defaults from contracts/parity.
  help?: {
    disclaimer?: string;
    sectionOrder?: string[];
    showAliases?: boolean;
  };
}
```

### createCLI

```ts
function createCLI(cfg: CLIConfig): CLIResult

interface CLIResult {
  program: Command; // configured Commander root
  theme: Theme;     // derived from cfg.accent, or the Neon default
}
```

`program` is a Commander `Command` pre-configured to the hop-top CLI
contract:

- No help/completion subcommands; `-h`/`--help` flag only.
- `-v, --version` prints `<name> v<version>` and exits.
- Global options: the `--format` suite (`--format`, `--format-opt`,
  `--format-help`, `--cols`/`--columns`, `--template`, `-o`/`--output`),
  plus `--quiet`, `--no-color`, `--no-hints` and `--offline`. Each is
  suppressible through `disable`.
- `showHelpAfterError` enabled.

### Command groups

#### Declaring groups

Groups are declared inline on `CLIConfig.groups`; there is no
exported `GroupConfig` type to import.

```ts
groups?: Array<{
  id: string;       // unique identifier (e.g. "management")
  title: string;    // display title (e.g. "MANAGEMENT")
  hidden?: boolean; // omitted or false = shown in default --help
}>
```

There are no default groups. Declare every group you intend to use;
an unrecognised `groupId` leaves the command in the ungrouped
`commands` section.

#### setCommandGroup

```ts
function setCommandGroup(cmd: Command, groupId: string): void
```

Assigns a subcommand to a named group. Commands without assignment
default to `commands`.

```ts
const { program } = createCLI({
  name: 'mytool', version: '1.0.0', description: '...',
  groups: [{ id: 'management', title: 'MANAGEMENT', hidden: true }],
});
const configCmd = program.command('config').description('Manage configuration');
setCommandGroup(configCmd, 'management');
```

#### `--help-all`

Root-level boolean option. When passed, the help formatter includes
commands from hidden groups. It is registered only when at least one
declared group sets `hidden: true`; with no hidden group there is
nothing for it to reveal and the flag does not appear.

```
$ mytool --help          # shows COMMANDS only
$ mytool --help-all      # shows COMMANDS + MANAGEMENT
```

### Output

Package: `@hop-top/kit/output`

```ts
function render(w: NodeJS.WritableStream, format: string, v: unknown): void
```

`render` looks the format up in `defaultRegistry` and throws on an
unknown one. The built-ins are `json`, `yaml`, `table`, `csv` and
`text`, each also exported as a formatter (`jsonFormatter`,
`yamlFormatter`, `tableFormatter`) and registrable through
`newRegistry` / `Registry` for custom formats.

```ts
import { render } from '@hop-top/kit/output';

render(process.stdout, 'json', { id: 1, name: 'row' });
```

`registerOutputFlags(program)` installs the `--format` suite;
`createCLI` calls it for you unless `disable.format` is set.

## Related pages

- [`cli-api-reference.md`](cli-api-reference.md) — Go equivalent
- [`py-api-reference.md`](py-api-reference.md) — Python equivalent
- [`cli-parity-guide.md`](../guides/cli-parity-guide.md) — required flags + parity contract
