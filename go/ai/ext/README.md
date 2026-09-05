# ext

Extension runtime for kit-based tools: the capability model in `ext.go`, the
init/close orchestration in `manager.go`, and one sub-package per capability.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`registry/`](registry/README.md) | in-process registration for `CapRegistry` extensions compiled into the binary | an extension ships inside the tool |
| [`hook/`](hook/README.md) | lifecycle hook bus for `CapHook` extensions, priority-ordered dispatch | an extension reacts to tool lifecycle events |
| [`discover/`](discover/README.md) | PATH scanning for `<prefix>-*` binaries and `--ext-info` interrogation | plugins are separate executables |
| [`dispatch/`](dispatch/README.md) | cobra bridge mounting discovered plugins as subcommands | discovered plugins must run as `tool <plugin>` |
| [`config/`](config/README.md) | YAML-backed enable/disable state and per-extension settings | an operator turns extensions on or off |

## Conventions

- `ext.go` owns the `Extension` interface and the `Cap*` capability constants; sub-packages implement one capability each and do not import each other.
- `manager.go` routes by capability and owns init and close ordering.
- Extension model and wiring walkthrough: [`docs/contributors/architecture`](../../../docs/contributors/architecture/README.md).
