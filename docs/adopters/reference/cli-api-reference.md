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

Identity plus opt-in behaviour. Only `Name` is required; every other
field has a working zero value.

```go
type Config struct {
    Name    string // binary name (e.g. "mytool")
    Version string // semver (e.g. "1.2.3")
    Short   string // one-line description
    Accent  string // optional hex colour; ignored when Palette is set
    Palette Palette // overrides the brand colour pair outright
    Disable Disable // opt out of individual built-in global flags
    Globals []Flag  // extra persistent flags on the root command
    Help    HelpConfig // root --help layout

    // ChdirResolver is called when -C <target> is not a directory.
    // nil = path-only.
    ChdirResolver func(target string) (dir string, err error)
    Hooks         Hooks // additive PersistentPreRunE slots

    EnforceValidate       bool // Layer-A pre-flight at Execute; default true
    DisableValidate       bool // explicit opt-out
    ValidationFailureMode ValidationFailureMode
}
```

The struct carries further fields beyond these; `go doc
hop.top/kit/go/console/cli.Config` is the complete list.

`cli.New` sets `EnforceValidate` to true whenever `DisableValidate` is
false, so setting `EnforceValidate: false` on its own has no effect —
use `DisableValidate: true` to opt out.

#### Disable

```go
type Disable struct {
    Format   bool // suppress --format
    Quiet    bool // suppress --quiet
    NoColor  bool // suppress --no-color
    Hints    bool // suppress --no-hints
    Chdir    bool // suppress -C/--chdir
    Progress bool // suppress --progress-format
    Config   bool // suppress -c/--config
    DryRun   bool // suppress the global --dry-run
}
```

### Root

```go
type Root struct {
    Cmd     *cobra.Command
    Viper   *viper.Viper
    Config  Config
    Theme   Theme
    Hints   *output.HintSet
    Streams *StreamWriter    // stdout=data, stderr=human
    Auth    AuthIntrospector // defaults to NoAuth

    // Non-nil only when the matching option is used.
    Identity     *identity.Keypair // WithIdentity
    Mesh         *peer.Mesh        // WithPeers
    PeerRegistry *peer.Registry    // WithPeers
    PeerTrust    *peer.TrustManager // WithPeers
    IdemStore    idemstore.Store   // WithIdempotencyStore
}
```

#### New

```go
func New(cfg Config, opts ...func(*Root)) *Root
```

Returns a Root pre-configured to the hop-top CLI contract. The
variadic options are the `With*` helpers (`WithIdentity`, `WithPeers`,
`WithIdempotencyStore`, …). `NewE` is the variant that runs
`Validate` at construction and returns the error instead of exiting.

- The `help` subcommand is registered hidden; `-h`/`--help` is the
  advertised surface.
- Persistent flags in the parity contract: `--quiet`, `--no-color`,
  `--format`, `-V/--verbose`.
- Kit plumbing persistent flags, registered hidden and revealed by
  `--help-all`: `-C/--chdir`, `-c/--config`, and the rest of the
  output-flag suite (`--format-opt`, `--cols`, `--template`, …).
- Each is suppressed individually via `Config.Disable`.
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
    ID     string // cobra group ID (e.g. "management")
    Title  string // section header (e.g. "MANAGEMENT")
    Hidden bool   // true = excluded from default --help
}
```

#### HelpConfig.Groups

```go
type HelpConfig struct {
    Disclaimer   string   // appended to Short as the Long description
    SectionOrder []string // section order; empty = parity.json default
    ShowAliases  bool     // display command aliases in help
    Groups       []GroupConfig
}
```

Built-in groups, always present:

| ID | Title | Hidden |
|----|-------|--------|
| `""` (empty) | COMMANDS | false |
| `management` | MANAGEMENT | true |

The default group's ID is the **empty string**, not `"commands"`: it
is cobra's ungrouped bucket, rendered under the COMMANDS heading. Only
`management` is registered with `AddGroup`. `Config.Help.Groups` adds
further groups on top of these two.

In Go, `SectionOrder` is validated but not re-applied — fang owns the
help template and fixes COMMANDS before FLAGS. The field is consumed
by the TS and Python adapters.

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

Commands without a `GroupID` fall into cobra's ungrouped bucket,
rendered under COMMANDS.

#### `--help-all`

Boolean flag on the root command itself — a local flag, not a
persistent one, so it is not inherited by subcommands. `NoOptDefVal`
is `"true"`, so the bare `--help-all` form works. When set, the help
output includes commands from all groups (including hidden ones) and
also unhides kit's plumbing flags.

A `--help-<id>` flag is registered per group on the same terms, and
`help all` / `help <group>` are the subcommand equivalents.

```
$ mytool --help          # shows COMMANDS only
$ mytool --help-all      # shows COMMANDS + MANAGEMENT
```

### Theme

```go
type Theme struct {
    // Brand colors.
    Palette Palette

    // Semantic colors.
    Accent    color.Color
    Secondary color.Color
    Muted     color.Color
    Error     color.Color
    Success   color.Color
    Warn      color.Color

    // Pre-built styles.
    Title  lipgloss.Style
    Subtle lipgloss.Style
    Bold   lipgloss.Style
}
```

Colours are `image/color.Color`, not `lipgloss.TerminalColor`. Built
from the CharmTone palette: `Config.Palette` when set, otherwise Neon
tinted by `Config.Accent`.

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
mytool config paths --from <dir>         # resolve as if cwd were <dir>
```

Flags:

| Flag | Values | Default | Effect |
|---|---|---|---|
| `--format` | `text` \| `json` \| `yaml` | `text` | Output format. This is a narrower set than the root `--format`, which defaults to `table`; a root `--format=table` is picked up here and rejected as unknown. |
| `--from` | any directory path | `os.Getwd()` | Resolve the chain as if the working directory were this path. Not a scope filter: every rung is still printed. Applies to `config path` too, which then reports the winner for that directory. |

`--format` is read from the shared flag only when it was explicitly
set (`Changed`), so leaving the root flag alone gives `text` here.

The path data comes from the `Resolver` the CLI registers, which
returns an ordered slice of `ResolvedPath` (`{Path, Source, Scope,
Exists}`), highest-precedence first. There is no `wins` field: the
winner is the first entry with `exists: true`. Every kit CLI MUST
expose `path` and `paths`; see
`~/.ops/docs/cli-conventions-with-kit.md` §10.

## Related pages

- [`ts-api-reference.md`](ts-api-reference.md) — TypeScript equivalent
- [`py-api-reference.md`](py-api-reference.md) — Python equivalent
- [`cli-parity-guide.md`](../guides/cli-parity-guide.md) — required flags + parity contract
- [`help-rendering.md`](help-rendering.md) — help layout + customization
- [`inspect-config-paths.md`](../guides/inspect-config-paths.md) — task guide for `config path` / `config paths`
