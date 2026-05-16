# Getting Started: Build Your First hop-top CLI

Three language factories; identical user-facing behavior.
Pick your runtime, get the same contract: version, help,
global flags, themed output.

> **Skip the boilerplate**: `kit init <name>` generates a complete
> project (any combination of Go/TS/Python) with CI, lint, Makefile,
> and conformance wired up. This guide is for hand-rolling or
> learning the contract. See [kit-init.md](kit-init.md).

## Prerequisites

| Language   | Install                          | Min version |
|------------|----------------------------------|-------------|
| Go         | `go get hop.top/kit@latest`      | Go 1.26 (via `go.mod`) |
| TypeScript | `pnpm add @hop-top/kit`          | Node 20+    |
| Python     | `pip install hop-top-kit`        | Python 3.11+ |

## Go

### Minimal working example

```
package main

import (
    "context"
    "os"

    "hop.top/kit/go/console/cli"
)

func main() {
    root := cli.New(cli.Config{
        Name:    "mytool",
        Version: "0.1.0",
        Short:   "Does useful things",
    })
    if err := root.Execute(context.Background()); err != nil {
        os.Exit(1)
    }
}
```

This gives you: `-v`/`--version`, `-h`/`--help`, `--quiet`,
`--no-color`, `--format`, `--no-hints`. No help or completion
subcommands.

### Add subcommands

```
root.Cmd.AddCommand(serveCmd(), listCmd())
```

Where each function returns a `*cobra.Command`.

### Built-in global flags

Registered automatically by `cli.New`:

| Flag          | Viper key  | Default   |
|---------------|------------|-----------|
| `--quiet`     | `quiet`    | `false`   |
| `--no-color`  | `no-color` | `false`   |
| `--format`    | `format`   | `"table"` |
| `--no-hints`  | `no-hints` | `false`   |

Read them from `root.Viper`:

```
if root.Viper.GetBool("quiet") {
    // suppress non-essential output
}
fmt := root.Viper.GetString("format")
output.Render(os.Stdout, fmt, data)
```

### Custom accent color

```
root := cli.New(cli.Config{
    Name:    "mytool",
    Version: "0.1.0",
    Short:   "Does useful things",
    Accent:  "#E040FB",
})
```

Sets the theme command color. Default palette: Neon (grass
green `#7ED957`, neon pink `#FF00FF`).

### Themed output

Access `root.Theme` for semantic styles:

```
fmt.Println(root.Theme.Title.Render("Section Header"))
fmt.Println(root.Theme.Subtle.Render("muted text"))
fmt.Println(root.Theme.Bold.Render("emphasis"))
```

Theme fields: `Accent`, `Secondary`, `Muted`, `Error`,
`Success`, `Title` (style), `Subtle` (style), `Bold` (style).

### Structured output

Use `kit/output` for table/json/yaml rendering:

```
type Item struct {
    ID   string `table:"ID"   json:"id"`
    Name string `table:"Name" json:"name"`
}

output.Render(os.Stdout, root.Viper.GetString("format"), items)
```

Table rendering driven by `table` struct tag. JSON and YAML
pass through to standard encoders.

### Next-step hints

Register hints via `root.Hints`:

```
root.Hints.Add("launch", output.Hint{
    Message: "Run `mytool status` to check progress",
})
```

Hints auto-suppressed when: `--no-hints`, `--format json/yaml`,
non-TTY (piped), or `--quiet`.

## TypeScript

### Minimal working example

```
import { createCLI } from '@hop-top/kit/cli'

const program = createCLI({
    name: 'mytool',
    version: '0.1.0',
    description: 'Does useful things',
})

program.parse()
```

Returns a Commander `Command` with `-v`/`--version`,
`-h`/`--help`, `--format`, `--quiet`, `--no-color` pre-wired.

### Add subcommands

```
program
    .command('serve')
    .description('Start the server')
    .action(() => {
        // handler
    })

program
    .command('list')
    .description('List all items')
    .action(() => {
        const opts = program.opts()
        // opts.format => "table" | "json" | "yaml"
        // opts.quiet  => boolean
    })

program.parse()
```

### Read global flags

```
const opts = program.opts()
if (!opts.quiet) {
    console.log('Processing...')
}
```

### Custom flags

Add flags to specific subcommands via Commander API:

```
program
    .command('deploy')
    .option('--env <name>', 'Target environment', 'staging')
    .action((cmdOpts) => {
        // cmdOpts.env => "staging"
    })
```

## Python

### Minimal working example

```
from hop_top_kit.cli import create_app

app = create_app(
    name="mytool",
    version="0.1.0",
    help="Does useful things",
)
```

Returns a `typer.Typer` with `-v`/`--version` (eager),
completion flags disabled, `no_args_is_help=True`.

### Add subcommands

```
@app.command()
def serve(port: int = 8080):
    """Start the server."""
    print(f"Listening on :{port}")

@app.command()
def status():
    """Show current state."""
    print("OK")

if __name__ == "__main__":
    app()
```

### Custom flags

Typer uses function signatures as flag definitions:

```
@app.command()
def deploy(
    env: str = typer.Option("staging", help="Target env"),
    dry_run: bool = typer.Option(False, "--dry-run"),
):
    """Deploy to target environment."""
    if dry_run:
        typer.echo(f"Would deploy to {env}")
```

## What You Get

All three factories produce identical behavior:

```
$ mytool --version
mytool 0.1.0

$ mytool --help
Does useful things

USAGE
  mytool [command] [flags]

FLAGS
  -v, --version       Print mytool version and exit
      --format <fmt>  Output format (table, json, yaml)
      --quiet         Suppress non-essential output
      --no-color      Disable ANSI colour
      --no-hints      Suppress next-step hints
  -h, --help          Display help
```

Users and scripts cannot tell which runtime produced the output.

## Putting your CLI on `$PATH`

`make build` writes to `./bin/<tool>` and `make install` writes to
`$GOPATH/bin/<tool>`. Most users have neither directly on `$PATH`,
which is why kit-scaffolded projects ship a `make symlink` target
(backed by the `kit symlink` subcommand):

```sh
make symlink
```

The target builds first (it depends on `build`), then walks `$PATH`,
intersects it with a platform-specific list of user-bin candidate
directories, and links `./bin/<tool>` into the first writable hit.

| Platform        | Candidate dir order                                                       | Link kind                |
|-----------------|---------------------------------------------------------------------------|--------------------------|
| Unix (macOS, Linux, *BSD, MSYS2/Git Bash/WSL) | `$XDG_BIN_HOME` → `~/.local/bin` → `~/bin` | real symlink             |
| Native Windows  | `%USERPROFILE%\bin` → `%USERPROFILE%\.local\bin` → `%LOCALAPPDATA%\Programs` | `.cmd` shim              |

`/usr/local/bin` is intentionally skipped because it usually
requires `sudo`. Native Windows falls back to a `.cmd` shim because
`os.Symlink` there requires Administrator or Developer Mode.

The link points at `./bin/<tool>`, so every subsequent `make build`
auto-deploys without re-running the install step.

### Overrides

| Flag / variable                | Effect                                                                    |
|--------------------------------|---------------------------------------------------------------------------|
| `make symlink SYMLINK_DIR=...` | Skip the PATH walk and link into the given dir                            |
| `make symlink FORCE=1`         | Replace an existing link that points at a different target                |
| `kit symlink --target ...`     | Direct invocation; default target is `./bin/<basename(cwd)>`              |
| `kit symlink --name ...`       | Override the link name (default: basename of target, minus `.exe`)        |

### Idempotent and safe

* Re-running with the same target is a no-op (prints `already linked`).
* An existing link or shim with a different target is refused unless
  `--force` (or `FORCE=1`) is passed.
* If no candidate dir is on `$PATH`, the command exits non-zero with
  a hint to add `$HOME/.local/bin` to `$PATH` or pass `--dir`.

## Output formatting

`hop.top/kit/go/console/output` provides the shared output flag set
plus a registry of pluggable `Formatter` implementations. Calling
`output.RegisterFlags(cmd, v)` (or accepting the defaults via
`cli.New`) wires every flag below; `output.Dispatch(cmd, v, data)`
resolves them and renders.

### Built-in formatters

| `--format <key>` | Description                              | Extensions       |
|------------------|------------------------------------------|------------------|
| `table` (default)| Aligned columns from `table:""` tags     | —                |
| `json`           | Indented JSON                            | `.json`          |
| `yaml`           | YAML document                            | `.yaml`, `.yml`  |
| `csv`            | RFC 4180 CSV                             | `.csv`           |
| `text`           | Plain text (`kv` / `lines` / `paragraph`)| `.txt`           |

### Formatter help

`--format-help` lists every registered formatter plus its key,
extensions, and option specs:

```
$ mytool list --format-help
table   Aligned columns
json    Indented JSON
yaml    YAML document
csv     RFC 4180 CSV
        delimiter (string, default ",")  Field delimiter
        no-header (bool,   default false) Suppress header row
text    Plain text
        style     (enum: kv|lines|paragraph) Layout
```

Pass a key to scope the help to one formatter:

```
$ mytool list --format-help csv
csv  RFC 4180 CSV   extensions: .csv
  delimiter (string, default ",")  Field delimiter
  no-header (bool,   default false) Suppress header row
```

### Per-format options

`--format-opt key=value` is repeatable; values are validated against
the active formatter's `Options()` spec.

```
# Use semicolon delimiter for CSV
$ mytool list --format csv --format-opt delimiter=';'

# Render text in paragraph style
$ mytool list --format text --format-opt style=paragraph

# Boolean keys may omit the value (treated as true)
$ mytool list --format csv --format-opt no-header
```

Unknown options, type mismatches, and out-of-enum values fail with a
concrete error listing the valid set.

### Column projection

`--cols` (alias `--columns`) restricts output to a subset of
`table:""` headers. Repeatable; each value can itself be
comma-separated; duplicates dedupe; column order matches struct
order, not flag order.

```
$ mytool list --cols Name,Status
$ mytool list --cols Name --cols Status        # equivalent
```

Unknown header names error out with the valid list. Works for
table / json / yaml / csv; for json/yaml the projected output is
a slice of maps keyed by header.

### Templates

`--template <go-tmpl>` runs a `text/template` against an input of
the form `{Items, Cols, Data}` where `Items` is `[]map[string]any`
projected over `table:""` fields. Mutually exclusive with `--cols`.

```
$ mytool list --template '{{range .Items}}{{.Name}}: {{.Status}}{{"\n"}}{{end}}'
```

### Output destination

`--output <path>` (`-o`) writes to a file instead of stdout. The
sentinel `-` (or empty) means stdout. The file extension infers the
format when `--format` is left at its default; an explicit `--format`
that disagrees with the extension errors out.

```
# Defaults: extension picks csv, file written, stdout untouched
$ mytool list -o report.csv

# Explicit --format overrides extension only when they agree;
# mismatch errors:
$ mytool list -o report.csv --format json
Error: format "json" does not match output extension ".csv"
       (use -o file.json or --format csv)

# Sentinel writes to stdout (useful for shell composition)
$ mytool list -o -
```

### Worked example

```
$ kit migrate status -o report.csv --cols SCHEMA,CURRENT,TARGET
$ cat report.csv
SCHEMA,CURRENT,TARGET
testdb,1.0.0,1.1.0
otherdb,2.3.4,2.4.0
```

The same data, in JSON, projected and piped:

```
$ kit migrate status --format json --cols SCHEMA,CURRENT
[
  {"SCHEMA":"testdb","CURRENT":"1.0.0"},
  {"SCHEMA":"otherdb","CURRENT":"2.3.4"}
]
```

### Styled tables

`output.WithTableStyle` opts a single `Render` call into the
lipgloss-backed table renderer. The styled path activates only when
the writer is a TTY; pipes, files, and test buffers always fall
through to the existing tabwriter renderer so command output stays
ANSI-free and diff-friendly.

Build a `TableStyle` from your CLI's active theme via the
`Root.TableStyle()` helper:

```go
output.Render(w, output.Table, rows,
    output.WithTableStyle(root.TableStyle()))
```

Mark specific rows for themed emphasis with `RowEmphasis`:

```go
output.Render(w, output.Table, rows,
    output.WithTableStyle(root.TableStyle()),
    output.RowEmphasis(0, output.EmphasisPrimary),
    output.RowEmphasis(2, output.EmphasisMuted),
)
```

`TableStyle` takes `color.Color` values directly — the `output`
package never imports `cli`, so adopters with custom themes can
construct a `TableStyle` literal without going through `Root`.

### Adopter checklist

* New subcommand: call `output.Dispatch(cmd, v, data)` instead of
  hand-rolled rendering — gets every flag above for free.
* Custom formatter: implement `output.Formatter`, then
  `output.Default.Register(myFmt)` (or build an isolated
  `output.NewRegistry()` and pass `output.WithRegistry(r)` to
  `RegisterFlagsWith`).
* Suppress `-o` for stream-only commands:
  `output.RegisterFlagsWith(cmd, v, output.DisableOutputFlag())`.
* Themed tables: `output.Render(w, output.Table, rows,
  output.WithTableStyle(root.TableStyle()))` — TTY only, no-op on
  pipes/files.

See [`go/console/output/README.md`](../../../go/console/output/README.md)
for the package-level API surface.

## Aliases

Map short names to longer command paths. Aliases expand at
dispatch time, so `kit d` runs `kit deploy` (and any args
passed after the alias are appended to the target).

> **Opt-in.** `cli.New()` does not mount the `alias` command
> automatically. Tools that want user-managed aliases must
> construct an `alias.Store` and add `root.AliasCmd(store)`
> themselves — see [Programmatic registration](#programmatic-registration)
> below.

### Built-in `alias` command

Once mounted via `root.AliasCmd(store)` (where `store` is an
`alias.Store` — see [Alias API](../reference/alias-api.md)):

```
$ kit alias add d deploy
alias d → deploy

$ kit alias add ds "deploy staging"
alias ds → deploy staging

$ kit alias list
ALIAS  TARGET
d      deploy
ds     deploy staging

$ kit alias remove d
removed alias d
```

`kit alias` with no args is equivalent to `kit alias list`.
`remove` accepts `rm` as a shorthand. `list` honours the
global `--format` flag (table/json/yaml).

### Targets with multiple words

The `add` subcommand joins all args after the name with
spaces, so quoting is optional:

```
$ kit alias add ds deploy staging   # same as "deploy staging"
```

Targets are resolved against the live command tree at startup;
unknown paths surface as a single aggregated error.

### Storage

Aliases persist to a YAML file managed by `alias.Store`. The
file location is whatever path you pass to `alias.NewStore`
— typical choice is `$XDG_CONFIG_HOME/<tool>/aliases.yaml`.
Missing files are not an error; the store stays empty until
the first `add`.

```yaml
# aliases.yaml
d: deploy
ds: deploy staging
```

### Programmatic registration

For aliases shipped with the binary (as opposed to user
aliases), register directly against a resolved `*cobra.Command`:

```go
deploy := deployCmd()
root.Cmd.AddCommand(deploy)

if err := root.Alias("d", deploy); err != nil {
    log.Fatal(err)
}
```

Or load both at once from the store at startup:

```go
store := alias.NewStore(aliasPath)
_ = store.Load()
root.Cmd.AddCommand(root.AliasCmd(store))
if err := root.LoadAliasStore(store); err != nil {
    log.Println(err) // collisions / unknown targets
}
```

`LoadAliases()` does the equivalent from a Viper `aliases:`
map if you prefer config-driven aliases.

### Hidden `aliases` listing

`root.AliasesCmd()` exposes a hidden `aliases` subcommand
that lists every alias currently registered against the
running command tree (built-in + store + config). Useful for
shell completion or debugging:

```
$ kit aliases --format json
[{"alias":"d","target":"deploy"}]
```

## Next Steps

* [CLI Parity Guide](cli-parity-guide.md) — full contract spec
* [SetFlag / TextFlag API](../reference/setflag-textflag-api.md) —
  multi-value flag types
* [Spaced Showcase](../concepts/spaced-showcase.md) — example app
  demonstrating all kit packages
