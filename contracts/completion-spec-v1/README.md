# completion-spec-v1

## What it answers

A kit CLI's completion specs, byte for byte: the Fig module (`<tool>
completion fig`) and Usage KDL (`<tool> completion usage`) of each tree:
`tree.json`, and `tree-tera.json` (a bin with Tera delimiters).

## Use it when

- you port the exporter to another SDK: rebuild each tree in that
  framework, export, compare against its two expected files
- you change an emitter in Go: edit the expected file first, then the code

## Quick start

```sh
make test-parity-completion-spec
# or:
go test ./go/console/cli/completionspec/ -run '^TestConformanceCompletionSpec'
```

## Contract

Go is canonical; the expected files are the truth. Strict runner: unknown
fields, a `version` other than `v1`, or a file with no runner fail it.

Each tree is cobra-shaped. Command: `use` (name, then operands),
`short`, `version`, `aliases`, `hidden`, `deprecated`, `annotations`
(`kit/args`), `valid_args` (`value\tdescription` allowed),
`valid_args_function` (a dynamic positional completer), `no_args`
(`cobra.NoArgs`), `flags`, `commands`. Flag: `name`, `short`, `type`
(`bool count string int duration stringSlice stringArray`), `usage` (raw,
backquotes included), `persistent`, `hidden`, `deprecated` (also hides),
`required`, `no_opt_default`, `enum` (kit flag enum), `filename_ext`
(`MarkFlagFilename`; empty means any file), `dirname`, `completion_function`.

Cobra-side facts, which a runner in another framework builds into its
tree rather than reimplementing in its walker: `use` operands (`<x>`,
`[x]`, bare, `...` variadic, a multi-word bracket joined without spaces;
`[flags]`, `[command]` and flag syntax skipped), `kit/args` (comma list,
`?` optional, `...` variadic, `...?` both), pflag `UnquoteUsage` (the first backquoted
word names the value; else the type, `stringSlice` as `strings`), and
`deprecated` meaning hidden.

Walker and emitter rules:

- Sorted by name (byte order): commands, flags. Aliases keep their order.
- Per command: its own and persistent flags. The `help` FLAG is dropped;
  a `help` command is kept. A root with `version` and no version flag gets
  `--version` (short `v` unless taken, help `version for <root>`), and
  Usage gets a top-level `version` node.
- No value: `bool`, `count`. Repeatable: `count`, `*Slice`, `*Array`,
  `stringTo*`. `no_opt_default`: value optional and only attached: Fig
  `requiresSeparator: true` with `isOptional`; Usage `<name>`,
  `default_missing`, `require_equals=#true` and `complete type="none"`.
- Positionals: none under `no_args`. A positional completer with no
  positional gets `[arg]`, optional and variadic.
- Candidates, first wins: `enum`, `valid_args` (cut at the tab, on every
  positional), `filename_ext` (dots dropped), `dirname`, a completer
  (a `__complete` callback). None: nothing written; Usage then falls back
  to files, Fig offers nothing.
- JS strings: `\\ \" \n \r \t`; other C0 and DEL as `\u00xx`; U+2028/9
  as `\u2028`/`\u2029`; all else raw UTF-8. KDL v2 strings: the same five,
  `\b \f`, and `\u{x}` (lowercase, unpadded) for other C0, DEL, NEL,
  U+200E/F, U+202A–E, U+2066–9, U+2028/9, U+FEFF. Header comments carry
  the escaped bin without quotes.
- Usage `run` bin: bare if `[A-Za-z0-9._+-]+`, else single-quoted (`'` as
  `'\''`); with a brace, `{% raw %}…{% endraw %}` per piece, cut in each `{%`.

## Runners

| Port | Runner |
|------|--------|
| Go | `go/console/cli/completionspec/conformance_test.go` |
| Rust | `sdk/experimental/rs/tests/cli_completion_spec_conformance.rs` |

## See also

- [`go/console/cli/completionspec/`](../../go/console/cli/completionspec/README.md)
