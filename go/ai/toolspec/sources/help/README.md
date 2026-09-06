# help

## What it answers

What a binary on PATH says about itself in `--help`, turned into commands,
flags and deprecation hints. Wrong package when the tool is kit-powered
(`hop.top/kit/go/ai/toolspec/cli` emits the authoritative manifest) or when
you only have a completion script (`hop.top/kit/go/ai/toolspec/sources/completion`).

## Use it when

- you resolve a tool by running it: `(&help.HelpSource{}).Resolve("kubectl")`, default deadline `help.DefaultTimeout` (5s)
- you already captured help text: `help.ParseHelpOutput(name, text)`
- you want deprecation, output-schema and state-introspection heuristics applied to an existing spec: `help.EnrichFromHelp(spec, text)`

## Contract

- Reads: stdout and stderr of `<tool> --help`, executed with `exec.CommandContext` under the timeout. A non-zero exit is tolerated when output is non-empty.
- Trust: medium. The text comes from the tool itself, but section detection is regex-based (`Commands`, `Flags`, ALL-CAPS headers) and unusual layouts drop entries silently.
- Executes a subprocess: only point it at binaries you would run by hand.
- No snippet here: the source runs an external tool.

## Neighbours

- `hop.top/kit/go/ai/toolspec/sources/completion`: static alternative when the binary is not available.
- `hop.top/kit/go/ai/toolspec`: `Registry` and `ChainSources` to combine this with other sources.

## See also

- [ToolSpec API reference](../../../../../docs/adopters/reference/toolspec-api.md)
