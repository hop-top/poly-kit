# completion

## What it answers

"How do I give a flag or positional argument dynamic shell completions
without hand-writing cobra's `ValidArgsFunction` signature?" Generating the
shell scripts themselves (`completion bash|zsh|fish`) is cobra's built-in
command, not this package.

## Use it when

- fixed choice list → `completion.StaticValues("a", "b")` or `completion.Static(items...)`
- values computed at completion time → `completion.Func(func(ctx, prefix) ([]Item, error))`
- `dimension:value` syntax (labels, scopes) → `completion.Prefixed("env", values)`
- config key names → `completion.ConfigKeys(viper.GetViper())`
- file or directory paths → `completion.File(".yaml")` / `completion.Dir()`
- attach to a flag → `completion.BindFlag(cmd, "env", c)`
- attach to positional args → `completion.BindArgs(cmd, c)`
- share completers across a command tree → `completion.NewRegistry()`, `Register`, `RegisterArg`

## Quick start

```go
cmd := &cobra.Command{Use: "deploy <target>", Run: func(*cobra.Command, []string) {}}
targets := completion.Prefixed("env", completion.StaticValues("prod", "preview", "staging"))
completion.BindArgs(cmd, targets)

// Drive cobra's hidden completion entry point the way a shell would.
cmd.SetOut(os.Stdout)
cmd.SetArgs([]string{cobra.ShellCompNoDescRequestCmd, "env:pr"})
_ = cmd.Execute()
```

Prints `env:prod`, `env:preview`, then cobra's directive line `:4`.

## Contract

- Prefix matching in `Static`, `StaticValues`, `ConfigKeys`, and the
  dimension half of `Prefixed` is case-insensitive. Empty prefix returns
  every item.
- `Prefixed` with no colon suggests `dimension:` only; with a colon it
  delegates the remainder to the inner completer and re-prepends
  `dimension:` to each result.
- Bridge output: `Item.Description` is joined to `Value` with a tab, cobra's
  wire format. Directive is `ShellCompDirectiveNoFileComp` on success,
  `ShellCompDirectiveError` when the completer returns an error.
- `File` and `Dir` return no items; they only emit
  `ShellCompDirectiveFilterFileExt` / `ShellCompDirectiveFilterDirs`. Use them
  through `BindFlag`/`BindArgs`, not via `Complete` directly.
- `BindFlag` ignores the error from `RegisterFlagCompletionFunc`; binding an
  unknown flag name is silent.
- Completers receive `cmd.Context()`, which is nil unless the host set one.

## Neighbours

- `hop.top/kit/go/console/cli`: root command that owns the cobra tree these
  completers bind to.
- `hop.top/kit/go/console/cli/config`: `config path|paths` subcommands, a
  common `ConfigKeys` host.

## See also

- [Completion user guide](../../../../docs/adopters/guides/completion-user-guide.md)
- [Completion API reference](../../../../docs/adopters/reference/completion-api.md)
- [Go primitives reference](../../../../docs/adopters/reference/go-primitives.md)
