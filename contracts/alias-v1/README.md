# alias-v1

## What it answers

How kit's alias bridge behaves in every port: when an alias is a native framework alias or a proxy command, what a proxy looks like (visibility, description), what `<alias> --help` shows, how the alias dispatches (to its target, to a custom handler, or not at all), what shell completion offers for it, which names a standalone validator refuses, and which cycles an alias map holds. Wrong place for alias storage and file formats (stores, config merging); those stay per port.

## Use it when

- you add or change a bridge option in any port: add a vector first, then make every runner pass it
- you port the alias bridge to a new SDK: load these vectors before anything else
- you change name validation or cycle detection anywhere: the four ports must keep agreeing

## Quick start

```sh
make test-parity-alias
```

## Contract

The vectors are the source of truth: a runner failure means the implementation drifted, not that the vector should be edited. Every file carries `version: "v1"` and a `description` stating the rules it pins. Runners are strict: unknown fields, another `version` and a corpus file no runner consumes all fail the run.

| File | Pins |
|------|------|
| `validate.json` `cases[] {alias, builtins?, existing?, expect}` | standalone name validation; `expect` is `ok` or the first failing rule: `empty`, `whitespace` (Unicode White_Space), `builtin`, `duplicate` |
| `cycles.json` `cases[] {aliases, cycles}` | cycle detection: an alias points at another when its target's first word is an alias; each cycle is a path from its smallest name back to it, cycles sorted by first name |
| `bridge.json` `tree`, `describe_template`, `cases[] {aliases, options?, proxy_flags?, skip?, checks}` | bridging onto the fixture `tree`; see below |

### bridge.json

Each case builds `tree` (`app` with `deploy --env` and `router start`), registers `aliases` sorted by name, adds `proxy_flags` (`{name, short}`, each taking a value) to every proxy as an adopter would after bridging, then runs each check on a fresh tree. Leaf commands record the path that ran, `--env` (`null` where the leaf has none) and their positional arguments.

| Check | Observable |
|-------|------------|
| `entry` `{kind, hidden?, description?}` | `native`: the framework's own alias on the target; `proxy`: a root command named after the alias, with its hidden flag and description |
| `run` `{ran: "target", path, env, args}` | the target ran with those values |
| `run` `{ran: "handler", name, target, args}` | the custom dispatch handler ran with the alias, its target path and every argument after the alias, unparsed |
| `run` `{ran: "none"}` | nothing ran (register-only) |
| `help` `"alias" \| "target"` | `<alias> --help` printed the proxy's own help, or the target's |
| `complete` `[words]`, `candidates` `[values]` | the shell completion request `__complete <words...>` (the last word is the one being completed) offers exactly `candidates`, descriptions dropped, in any order; an alias listed twice fails |

`options` maps onto each port's options: `hidden`, `describe: true` (a describe function rendering `describe_template`), `help: "own" | "passthrough"`, `dispatch: "handler" | "register-only"`. Without `options` a port bridges as it always has. With any option set, every alias becomes a proxy.

A visible proxy, or one with a custom dispatch handler, is a real command: completion lists a visible one once among the subcommands and offers its own flags. A hidden proxy that dispatches its target or runs nothing is a stand-in, listed by name as `alias for <target>` where the port lists hidden aliases.

### Per-port defaults

Defaults are each port's existing behaviour and are not unified; `by_port` on a check records where a port's default differs from the check's value.

| | Go | TS | Python | Rust |
|---|---|---|---|---|
| direct child / single-word target | native | native | native | native |
| deeper / multi-word target | hidden proxy | hidden proxy | native to the first word | hidden proxy |
| proxy `--help` | own | passthrough | passthrough | own |
| proxy dispatch | target's `RunE` on the proxy | re-enters the program | invokes the target path | caller, via `Root::alias_target` |

### Skips

A case's `skip` maps a port to the reason it does not run the case:

- Python skips `defaults: nested target`: its default bridge resolves a multi-word target to its first word only.
- Rust skips the custom-dispatch and register-only cases: it has no dispatch hook, every proxy is register-only and the caller dispatches. The Rust runner plays that caller, re-parsing the target path with the proxy's arguments.
- Rust skips the `proxy_flags` cases: `Root::alias_with` builds the proxy inside the root's tree and exposes no way to add flags to it.

## Runners

| Port | Runner |
|------|--------|
| Go | `go/console/alias/conformance_test.go` (`TestConformanceAliasValidate`, `TestConformanceAliasCycles`), `go/console/cli/alias_conformance_test.go` (`TestConformanceAliasBridge`) |
| TypeScript | `sdk/ts/src/alias.conformance.test.ts` |
| Python | `sdk/py/tests/test_alias_conformance.py` |
| Rust | `sdk/experimental/rs/tests/cli_alias_conformance.rs` (feature `cli`) |

## See also

- [Alias API reference](../../docs/adopters/reference/alias-api.md)
- [`go/console/cli/alias.go`](../../go/console/cli/alias.go), [`sdk/ts/src/alias.ts`](../../sdk/ts/src/alias.ts), [`sdk/py/hop_top_kit/alias.py`](../../sdk/py/hop_top_kit/alias.py), [`sdk/experimental/rs/src/cli/alias.rs`](../../sdk/experimental/rs/src/cli/alias.rs)
