# completionspec

## What it answers

"How does a terminal that reads completion specs, not shell scripts,
complete my kit CLI?" It exports the live command tree as a Fig spec (VS
Code terminal suggest, Kiro CLI, inshellisense, Warp) and as a Usage KDL
spec ([usage.jdx.dev](https://usage.jdx.dev), mise). Shell scripts
(`completion bash|zsh|fish|powershell`) are cobra's; completers themselves
are [`completion/`](../completion/README.md).

## Use it when

- a kit `Root` → nothing to do: `<tool> completion fig` and
  `<tool> completion usage` are registered next to the shell scripts
- a bare cobra tree → `completionspec.Register(completionCmd)`, or
  `RenderFig` / `RenderUsage` over `FromCobra(root)`
- a value should be in the spec itself → `Root.WithFlagEnum`, cobra
  `ValidArgs`, `MarkFlagFilename`, `MarkFlagDirname`

## Quick start

```go
root := &cobra.Command{Use: "tool", Short: "Example tool"}
deploy := &cobra.Command{Use: "deploy <env>", Short: "Deploy", ValidArgs: []string{"prod", "staging"}}
root.AddCommand(deploy)
fmt.Print(completionspec.RenderUsage(completionspec.FromCobra(root)))
```

Prints a KDL spec with `cmd "deploy"` and `choices strict=#false "prod" "staging"`.

## Contract

- Fig output is an ES module, `export default completionSpec`, with no type
  annotations (JSDoc only): it loads as JavaScript and compiles as
  TypeScript. JSON is not an option: generators are functions.
- Values a completion function computes are never snapshotted. Fig gets a
  generator running `<bin> __complete <tokens>`; Usage gets
  `complete run="<bin> __complete …" descriptions=#true`. Both parse cobra's
  `value\tdescription` lines and stop at the `:<directive>` line, so the
  directive (file filtering, no-space) is not carried over. Use
  `MarkFlagFilename` / `MarkFlagDirname` for spec-visible paths: a
  `completion.File` / `completion.Dir` completer is a function, so it calls
  back like any other.
- Hidden and deprecated commands, and hidden flags, are kept and marked
  hidden: they still parse. Inside a kit `Root` the management group is
  hidden as it is in `--help`. The help flag is left out.
- Choices are advisory (`strict=#false`): kit enums and `ValidArgs` reject
  nothing.
- Output is byte-stable and sorted by name.
  [`contracts/completion-spec-v1`](../../../../contracts/completion-spec-v1/README.md)
  pins it for every port.

## Neighbours

- [`completion/`](../completion/README.md): completers bound to flags and
  positionals; what the generators call back into.
- `hop.top/kit/go/ai/toolspec/cli`: the command tree as an agent tool spec,
  not a completion spec.

## See also

- [Completion user guide](../../../../docs/adopters/guides/completion-user-guide.md)
- [Flag enums](../../../../docs/adopters/reference/flag-enums.md)
