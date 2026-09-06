# completion

## What it answers

Command names and flags recovered from a tool's shell completion script,
without executing the tool. Wrong package when the binary is available and
you want richer text (`hop.top/kit/go/ai/toolspec/sources/help`).

## Use it when

- you have a zsh script: `completion.ParseZshCompletion(name, script)` yields commands (`'name:description'` entries) and flags (`--flag[Description]`, `(-s --long)` forms)
- you have a bash script: `completion.ParseBashCompletion(name, script)` yields command names only

## Quick start

```go
script := `_demo_commands=(
  'list:List items'
  'add:Add an item'
)
_arguments '--verbose[Print more output]'`
spec := completion.ParseZshCompletion("demo", script)
for _, c := range spec.Commands {
    fmt.Println("command:", c.Name)
}
for _, f := range spec.Flags {
    fmt.Println("flag:", f.Name, f.Description)
}
// Output:
// command: list
// command: add
// flag: --verbose Print more output
```

Verified by `example_test.go` in this directory.

## Contract

- Reads: the script text only; no I/O, no subprocess.
- Trust: medium. Names are exact; descriptions exist only where the script carries them; safety, arguments and semantics are never inferred.
- Flag names keep their leading dashes as written in the script.

## Neighbours

- `hop.top/kit/go/ai/toolspec/sources/help`: fills descriptions and sections this source cannot see.
- `hop.top/kit/go/console/cli/completion`: generates completion scripts for kit-powered tools; this package only reads them.

## See also

- [ToolSpec API reference](../../../../../docs/adopters/reference/toolspec-api.md)
