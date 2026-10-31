# cli/completion

## What it answers

How does a flag or positional argument of a kit-rs CLI get dynamic shell completions, the way Go `console/cli/completion` gives them to cobra? The command tree is `hop_top_kit::cli::Root`, built from kit-owned `CommandSpec`s; the parsing provider (clap) is adapter-internal.

## Use it when

- fixed choice list → `static_values(["a", "b"])` or `static_items(items)`
- values computed at completion time → `func(|ctx, prefix| Ok(items))`
- `dimension:value` syntax → `prefixed("env", values)`
- config key names → `config_keys(value)` (a key list, or a `config::Value` with feature `config`)
- file or directory paths → `file([".yaml"])` / `dir()`
- attach to a flag → `bind_flag(&mut root, "deploy", "env", c)`; to positional args → `bind_args(&mut root, "deploy", c)`
- a closed value set for a flag → `Root::with_flag_enum`, or a hard closed set with `FlagSpec::allowed_values`

## Quick start

```rust
use hop_top_kit::cli::completion::{bind_args, prefixed, static_values};
use hop_top_kit::cli::{CommandSpec, Root};

let mut root = Root::new(CommandSpec::new("tool").subcommand(CommandSpec::new("deploy")));
bind_args(&mut root, "deploy", prefixed("env", static_values(["prod", "preview", "staging"])));

let (mut out, mut err) = (Vec::new(), Vec::new());
root.try_builtin(["tool", "__completeNoDesc", "deploy", "env:pr"], &mut out, &mut err)
    .unwrap()
    .unwrap();
assert_eq!(String::from_utf8(out).unwrap(), "env:prod\nenv:preview\n:4\n");
```

## Contract

- Prefix matching in the completers is case-insensitive; `prefixed` splits on the first colon. Descriptions join values with a tab; directives are cobra's integers (error 1, no file completion 4, extensions 8, directories 16).
- `completion bash|zsh|fish|powershell` writes cobra v1.10.2's scripts byte for byte; `__complete` / `__completeNoDesc` answer in cobra's wire format, so a Go and a Rust kit CLI complete alike.
- Parity: [`contracts/completion-v1`](../../../../../../contracts/completion-v1/README.md), run by `tests/cli_completion_conformance.rs`.
- The script templates derive from cobra (Apache-2.0, see the crate `NOTICE` and `scripts/LICENSE-cobra.txt`); regenerate them with `make completion-regen`, never by hand.

## Neighbours

- `hop_top_kit::cli::Root` (`../root.rs`): the kit root completers bind to, aliases and flag enums
- `hop_top_kit::config`: the config tree `config_keys` reads

## See also

- [Go `console/cli/completion`](../../../../../../go/console/cli/completion/README.md)
- [Completion user guide](../../../../../../docs/adopters/guides/completion-user-guide.md)
