# completion-spec-v1

## What it answers

What a kit CLI's exported completion specs look like, byte for byte: the
Fig module (`<tool> completion fig`) and the Usage KDL (`<tool> completion
usage`) for one fixture command tree. Wrong place for shell completion
scripts or completer behaviour; those are each framework's own.

## Use it when

- you port the exporter to another SDK: rebuild `tree.json` in that
  framework, export, compare against both expected files
- you change an emitter in Go: edit the expected file first, then the code

## Quick start

```sh
make test-parity-completion-spec
# or:
go test ./go/console/cli/completionspec/ -run '^TestConformanceCompletionSpec'
```

## Contract

Go is canonical; the expected files are the source of truth. The runner is
strict: unknown fields in `tree.json`, a `version` other than `v1`, and any
file in this directory without a runner fail the run.

`tree.json` is a cobra-shaped tree. Command: `use` (name, then operands),
`short`, `version`, `aliases`, `hidden`, `deprecated`, `annotations`
(`kit/args`), `valid_args` (`value\tdescription` allowed),
`valid_args_function` (a dynamic positional completer), `no_args`
(`cobra.NoArgs`), `flags`, `commands`. Flag: `name`, `short`, `type`
(`bool count string int duration stringSlice stringArray`), `usage` (raw,
backquotes included), `persistent`, `hidden`, `deprecated` (also hides),
`required`, `no_opt_default`, `enum` (kit flag enum), `filename_ext`
(`MarkFlagFilename`; empty means any file), `dirname`, `completion_function`.

Rules a port must reproduce:

- Commands and flags sorted by name (byte order); aliases keep their order.
- Hidden: `hidden`, or `deprecated` on a command or flag. Kept, marked hidden.
- Flags per command: its own plus its persistent ones; `help` dropped. A
  root with `version` and no `version` flag gets `--version`, short `v`
  unless taken, help `version for <root>`.
- Description and value name follow pflag `UnquoteUsage`: the first
  backquoted word names the value and loses its quotes; else the type
  (`stringSlice` is `strings`, `int64` is `int`, `float64` is `float`).
- No value: `bool`, `count`. Optional value: `no_opt_default` set.
  Repeatable: `count`, `*Slice`, `*Array`, `stringTo*`.
- Positionals: none under `no_args`; else `kit/args` (comma list, `?`
  optional, `...` variadic); else `use` operands: `<x>`, `[x]`, bare,
  `...` variadic, a multi-word bracket joined without spaces,
  `[flags]`/`[command]`/flag syntax skipped. A command with a positional
  completer and no positional gets `[arg]`, optional and variadic.
- Candidates, first wins: `enum`, `valid_args` (description cut at the tab,
  on every positional), `filename_ext` (leading dots dropped), `dirname`,
  then a completer as a callback into `__complete`.
- Strings: JS escapes `\\ \" \n \r \t`, other C0 and DEL as `\u00XX`, and
  U+2028/U+2029; KDL v2 adds `\b \f` and writes C0, DEL, NEL, U+200E/F,
  U+202A–E, U+2066–9, U+2028/9 and U+FEFF as `\u{x}`. The binary name in
  the Usage `run` is a bare shell word when it is `[A-Za-z0-9._+-]+`, else
  single-quoted.

## Runners

| Port | Runner |
|------|--------|
| Go | `go/console/cli/completionspec/conformance_test.go` |

## See also

- [`go/console/cli/completionspec/`](../../go/console/cli/completionspec/README.md)
