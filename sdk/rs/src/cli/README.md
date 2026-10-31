# cli

The kit CLI root (feature `cli`): the parts of Go `console/cli` a kit-rs CLI author needs: persistent root flags, `-v/--version`, help groups with `--help-all`, root aliases, flag-error surfaces, opt-in autocorrect, and shell completion. An author defines the tree with the kit-owned `CommandSpec` builder (`spec.rs`) and never names clap: clap is the parsing provider, confined to `adapter.rs` and the parse seams in `run.rs`, `usage.rs` and the completion walkers. The kit-owned command model (`model.rs`) is what other surfaces project.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`spec.rs`](spec.rs) | `CommandSpec`, `FlagSpec`, `ArgSpec`, `ValueSpec`: the builder (name, about, flags, args, groups, annotations, side-effect tier, handler) | you define a command |
| [`adapter.rs`](adapter.rs) | `CommandSpec` to clap: the only place the provider's builder is called | you swap or debug the parse provider |
| [`help.rs`](help.rs) | the help renderer: `Usage:` / `COMMANDS:` / group / `FLAGS:` layout of the TypeScript and Python kits over the kit model; `Theme`, `Palette` (Neon, Dark, Bauhaus) | you change help layout or colors |
| [`log.rs`](log.rs) | `Logger` (levels from `-V`, `--quiet` floor, themed lines on stderr), `Verbosity`, `Invocation::logger/verbose_count/is_quiet/hint_options` | a handler logs or reads verbosity |
| [`safety.rs`](safety.rs) | `safety_guard`: the `--force` guard for risky operations, stricter off a terminal | a handler must refuse a risky op without `--force` |
| [`config.rs`](config.rs) | `Config` (incl. the root-help `disclaimer` paragraph), `Disable`, `GlobalFlag`, `GroupConfig`: what `Root::with_config` builds from | you start a kit CLI |
| [`run.rs`](run.rs) | `Root::execute` / `run`: alias expansion, help-group flags, USAGE envelopes, autocorrect, handler dispatch; `Invocation`, `Globals` | a command must run |
| [`usage.rs`](usage.rs) | flag-error surfaces (unknown flag, missing value, unknown command, suggestions) and autocorrect | you change an error text |
| [`autocorrect_prompt.rs`](autocorrect_prompt.rs) | the `--autocorrect prompt` question and the per-mode side-effect gate | you change the prompt or its defaults |
| [`model.rs`](model.rs) | `Root::model()`: the kit-owned `CommandInfo` / `FlagInfo` tree a surface projects | a surface must read the tree without clap |
| [`root.rs`](root.rs) | `Root`: wraps the root `CommandSpec`; adds the hidden `completion` command unless a command or alias owns the name (or `disable_completion_command`); the `__complete` entry point | your CLI must offer shell completion |
| [`gate.rs`](gate.rs) | the delegation gates in Go's order: `--dry-run` enforcement, `--policy`, the `--confirm` matrix and typed token, the `--max-ops` account; `PromptSource` | a command must be confirmed, previewed or budgeted; an unannotated `delete`/`remove`/`rm`/`destroy`/`purge`/`drop` leaf is gated as destructive and under `--dry-run` kit renders the preview itself without running the handler, from the one `DESTRUCTIVE_NAMES` table in `model.rs` that the `/v1/commands` descriptor also reads |
| [`policy.rs`](policy.rs) | `Policy`, `Engine`, the loader and the shipped `kit-default` | you load or enforce a named policy |
| [`validate.rs`](validate.rs) | `Root::validate` and `validate_signature`, `ValidationError`, the failure modes | a tree must be checked at boot |
| [`validate_flag.rs`](validate_flag.rs) | `Root::with_flag_validator` | a flag value must be refused before the handler |
| [`alias_store.rs`](alias_store.rs) | `AliasStore` and the `alias` / `aliases` commands | a user manages aliases |
| [`paths_cmd.rs`](paths_cmd.rs) | `config path` and `config paths` | a user asks where the config comes from |
| [`plan.rs`](plan.rs) | `Plan`, `Effect`, `Invocation::render_plan` | a command previews under `--dry-run` |
| [`alias.rs`](alias.rs) | root aliases with Go's texts; a deep alias is Go's flagless shim; `alias_with` registers a proxy shaped by `AliasOptions`; `validate_alias_name`, `detect_cycles` | a short name must reach a nested command |
| [`enums.rs`](enums.rs) | flag value enums: completion values and the `(one of: …)` help suffix | a flag takes a closed set of values |
| [`completion_spec/`](completion_spec/README.md) | `completion fig` / `completion usage`: Fig and Usage KDL specs of the tree, `Spec::from_root`, `render_fig`, `render_usage` | a terminal reads a completion spec, not a shell script |
| [`completion/`](completion/README.md) | completer API (`static_values`, `func`, `prefixed`, `config_keys`, `file`, `dir`, `Registry`), `bind_flag` / `bind_args`, the scripts | a flag or argument needs dynamic values |

## Conventions

- Go `console/cli` is canonical; behaviour is pinned by [`contracts/cli-root-v1`](../../../../contracts/cli-root-v1/README.md) (root, flag errors, autocorrect), [`contracts/completion-v1`](../../../../contracts/completion-v1/README.md); aliases by [`contracts/alias-v1`](../../../../contracts/alias-v1/README.md).
- Ported: the root flags, version, `--help-all` / `--help-<group>`, the hidden `help [all|<path>|<group>]` form, group section titles and the themed help renderer (`Theme`, colored only on a terminal; `NO_COLOR`, `TERM=dumb` and `--no-color` win), aliases and the alias store, flag errors, autocorrect (`read` and `prompt`, asked on the controlling terminal), `-C` / `-c`, `--confirm` / `--max-ops` / `--policy`, `--dry-run`, flag validators, `Root::validate` and the signature validators, `config path|paths`. Also ported behind feature `cli-permissions`: `permission.rs`, the `permissions:` rule gate (`PermissionGate`; Go `celpermission`; `contracts/celpermission-v1`), and `served.rs`, the served-call gate (`ServedGate`: `callers:` rules, rules, adopter check, then the `max_ops` charge; `UsageLedger` over a `UsageStore`, in memory unless `with_usage_file` / `with_usage_store`; wired by `api_server`, `mcp` and `socket`). Not ported: Go's `GLOBAL FLAGS` split (subcommand help lists its own flags, as py/ts do), `--api-version`, the `api` / `auth` / `audit` command groups.
- A value-taking flag consumes the next token even when it starts with `-` (`--tag -x`, `--n -5`), as pflag does; a flag with `no_opt_default` takes its value as `--name=value` only.
- `execute` is re-entrant. `-C/--chdir` is applied for the duration of one run and the directory restored after (concurrent `-C` runs queue; the directory is process-wide). `-c/--config` is carried in `Globals` and layered by `Invocation::config_args` / `layer_config` (feature `config`).
- A kit root (`Root::with_config`) gates and validates; a bare `Root::new` wrapper does neither.
- The `management` group is `hide(true)` on the provider's command: clap has no command groups.
- `Root::command()` and the help builders are crate-private. Help text is `Root::render_help` / `render_help_all` / `render_help_group` / `render_command_help`; the tree is `Root::model()`.
