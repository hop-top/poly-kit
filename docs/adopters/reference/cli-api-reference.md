# Go CLI API Reference

> Reference for `hop.top/kit/go/console/cli`. Pairs with the
> [TS](ts-api-reference.md) and [Python](py-api-reference.md)
> references — same contract, native types per runtime.

## Who this is for

Go authors building a tool with kit's CLI factory. If you are
adopting kit for the first time, start with the
[top-level README](../../../README.md#go-tools).

## Before you begin

```bash
go get hop.top/kit@latest
```

```go
import "hop.top/kit/go/console/cli"
```

## Recommended path

```go
root := cli.New(cli.Config{
    Name:    "mytool",
    Version: "1.2.3",
    Short:   "does things",
    Accent:  "#E040FB", // optional per-tool accent
})
root.Cmd.AddCommand(serveCmd(), listCmd())
if err := root.Execute(context.Background()); err != nil {
    os.Exit(1)
}
```

`root.Execute(ctx)` runs the cobra command through fang, which
handles `--version`, styled help, and error formatting. Subcommands
read `root.Viper` for `quiet`, `no-color`, and `format`.

## Verify the result

```bash
mytool --help          # styled help, no help/completion subcommands
mytool --version       # "mytool 1.2.3"
mytool --help-all      # also shows hidden management groups
```

---

## Reference

### Config

```go
type Config struct {
    Name    string // binary name (e.g. "mytool")
    Version string // semver (e.g. "1.2.3")
    Short   string // one-line description
    Accent  string // optional hex colour (e.g. "#E040FB")
}
```

### Root

```go
type Root struct {
    Cmd    *cobra.Command
    Viper  *viper.Viper
    Config Config
    Theme  Theme
    Hints  *output.HintSet
}
```

#### New

```go
func New(cfg Config) *Root
```

Returns a Root pre-configured to the hop-top CLI contract:

- No help/completion subcommands.
- Persistent flags: `--quiet`, `--no-color`, `--format`.
- Version handled by fang (`-v`/`--version`).
- Styled help via fang colour scheme.

#### Execute

```go
func (r *Root) Execute(ctx context.Context) error
```

Runs the root command through fang. Handles version output, styled
help, error rendering, and man page generation.

### Command groups

#### GroupConfig

```go
type GroupConfig struct {
    ID     string // unique identifier (e.g. "management")
    Title  string // display title (e.g. "MANAGEMENT COMMANDS")
    Hidden bool   // true = excluded from default --help
}
```

#### HelpConfig.Groups

```go
type HelpConfig struct {
    Groups []GroupConfig
}
```

Default groups when none specified:

| ID | Title | Hidden |
|----|-------|--------|
| `commands` | COMMANDS | false |
| `management` | MANAGEMENT COMMANDS | true |

#### Assigning a command to a group

Use cobra's built-in `GroupID` field:

```go
cmd := &cobra.Command{
    Use:     "config",
    Short:   "Manage configuration",
    GroupID: "management",
}
root.Cmd.AddCommand(cmd)
```

Commands without a `GroupID` default to the `commands` group.

#### `--help-all`

Persistent boolean flag on the root command. When set, the help
template includes commands from all groups (including hidden ones).

```
$ mytool --help          # shows COMMANDS only
$ mytool --help-all      # shows COMMANDS + MANAGEMENT
```

### Theme

```go
type Theme struct {
    Accent    lipgloss.TerminalColor
    Dim       lipgloss.TerminalColor
    Success   lipgloss.TerminalColor
    Warning   lipgloss.TerminalColor
    Error     lipgloss.TerminalColor
    Command   lipgloss.TerminalColor
    Flag      lipgloss.TerminalColor
}
```

Built from CharmTone palette plus optional `Config.Accent`.

### Config inspection

Every kit-built CLI ships `config path` and `config paths` for free
once it registers the shared subcommand. Pair with the task guide
[`inspect-config-paths.md`](../guides/inspect-config-paths.md).

```go
import kitcliconfig "hop.top/kit/go/console/cli/config"

// Attach to the existing `config` parent command:
kitcliconfig.RegisterPathSubcommands(cfgCmd, "mytool")
```

Once registered:

```bash
mytool config path                       # winning file (single line)
mytool config paths                      # full chain
mytool config paths --format json|yaml   # machine-readable
mytool config paths --from <scope>       # filter to one scope
```

Flags:

| Flag | Values | Default | Effect |
|---|---|---|---|
| `--format` | `table` \| `json` \| `yaml` | `table` | Output format. Inherits `--format` from root if set. |
| `--from` | `default` \| `system` \| `user` \| `project` \| `flag` | (all) | Show only one scope row. `config path` ignores this — it always returns the winner. |

The path data is sourced from `core/config.Paths(cwd)`, which
returns an ordered slice of `ResolvedPath` (`{Scope, Source, Exists,
Wins}`). Every kit CLI MUST expose `path` and `paths`; see
`~/.ops/docs/cli-conventions-with-kit.md` §10.

## Related pages

- [`ts-api-reference.md`](ts-api-reference.md) — TypeScript equivalent
- [`py-api-reference.md`](py-api-reference.md) — Python equivalent
- [`cli-parity-guide.md`](../guides/cli-parity-guide.md) — required flags + parity contract
- [`help-rendering.md`](help-rendering.md) — help layout + customization
- [`inspect-config-paths.md`](../guides/inspect-config-paths.md) — task guide for `config path` / `config paths`
