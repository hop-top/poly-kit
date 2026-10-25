# cli/completion_spec

## What it answers

How does a terminal that reads completion specs, not shell scripts, complete a kit-rs CLI? `<tool> completion fig` prints a Fig spec (VS Code terminal suggest, Kiro CLI, inshellisense, Warp) and `<tool> completion usage` a Usage KDL spec ([usage.jdx.dev](https://usage.jdx.dev), mise), byte for byte what Go `console/cli/completionspec` prints for the same tree.

## Use it when

- a `hop_top_kit::cli::Root` → nothing to do: both leaves sit under the hidden `completion` command, next to the shell scripts
- the spec as a value → `render_fig(&Spec::from_root(&root))` / `render_usage(...)`
- a value should be in the spec itself → `Root::with_flag_enum`, clap possible values, a `file([...])` / `dir()` completer
- no built-in `completion` command → `Root::disable_completion_command`

## Quick start

```rust
use clap::{Arg, Command};
use hop_top_kit::cli::completion_spec::{render_usage, Spec};
use hop_top_kit::cli::Root;

let deploy = Command::new("deploy")
    .arg(Arg::new("env").required(true).value_parser(["prod", "staging"]));
let root = Root::new(Command::new("tool").subcommand(deploy));
let kdl = render_usage(&Spec::from_root(&root));
assert!(kdl.contains(r#"choices strict=#false "prod" "staging""#));
```

## Contract

- One model from the built clap tree, two emitters. Commands and flags are sorted by name; aliases keep their order. Hidden commands and flags are kept and marked hidden; the help flag is left out.
- A completer that is not `file` / `dir` becomes a callback: Fig's `cobraComplete` generator and Usage's `complete run="<bin> __complete …"`, both reading the `__complete` wire format. The directive is not carried over.
- A value flag whose `num_args` admits zero values takes it only attached: Fig `requiresSeparator`, Usage `require_equals` and `default_missing`, no candidates.
- Escaping (JS, KDL v2, the shell word and Tera guard of the Usage bin) is the contract's. Parity: [`contracts/completion-spec-v1`](../../../../../../contracts/completion-spec-v1/README.md), run by `tests/cli_completion_spec_conformance.rs`.
- clap differs from cobra in its version flag (`-V`) and has no `default_missing_value` getter; the walker reads it by parsing the bare flag.

## Neighbours

- [`../completion/`](../completion/README.md): the completers and the `__complete` entry the callbacks reach
- `hop_top_kit::cli::Root` (`../root.rs`): the tree and its bindings

## See also

- [Go `console/cli/completionspec`](../../../../../../go/console/cli/completionspec/README.md)
