# Delegation

## What it answers

What stops a delegated or scripted run from doing more than it was allowed to: which `kit/side-effect`
tier needs `--confirm`, a typed `--confirm-token`, a named `--policy` or a `--max-ops` budget, and what
`--dry-run` does on each tier. `Gate` runs those checks in Go's order before a leaf's handler; `Policy`,
`Engine` and `PolicyLoader` carry the policy; `Root` (in `HopTop\Kit\Cli`) wires it into a command tree.

## Use it when

- a command tree runs behind an agent or a script: build it on `HopTop\Kit\Cli\Root` and annotate leaves
- you embed the gate in your own runner: call `Gate::run()` with a `Leaf` and `Globals`
- a host runs several commands under one budget: share one `Gate::engine()` across `run()` calls

## Quick start

```php
// after require vendor/autoload.php
use HopTop\Kit\Cli\Delegation\{Gate, Globals, Leaf, PromptSource};

$gate = new Gate('tool', prompt: PromptSource::none());
$leaf = new Leaf('tool', ['rm'], ['kit/side-effect' => 'destructive']);
$gate->run($leaf, new Globals(confirm: 'yes'), fn (bool $dry) => 'ran'); // 'ran'
// new Globals() instead: GateRefusal, UNAUTHORIZED, exit 5 (no terminal, so confirm resolves to no)
```

## Contract

- Order: malformed tier, `--dry-run` table, `--policy`, confirm, handler, budget. A refusal is a
  `GateRefusal` carrying an unrendered `CliError`; the caller renders and exits
- An unannotated leaf named like `delete`, `rm` or `purge` is `destructive-shared`; an explicit annotation
  always wins. `--dry-run` on it renders a plan and never runs the handler
- `--policy` names a file under `$XDG_CONFIG_HOME/<program>/policies/`; `kit-default` is shipped. A bad
  name, a missing file or invalid YAML is `USAGE`; a refusal is `UNAUTHORIZED` and `--confirm=yes` does not lift it
- `permissions:` and `callers:` rules are validated, not evaluated
- Parity: `contracts/cli-root-v1` delegation and `infer` vectors; Go `go/console/cli` is the reference

## Neighbours

- `HopTop\Kit\Cli`: `Root`, `CommandSpec` and `SideEffect` (the one tier table)
- `HopTop\Kit\Output`: `CliError`, the envelope every refusal renders

## See also

- [PHP reference](../../../../../../docs/adopters/reference/php-sdk.md#cli-root-and-delegation-gate)
- [`cli-root-v1`](../../../../../../contracts/cli-root-v1/README.md)
