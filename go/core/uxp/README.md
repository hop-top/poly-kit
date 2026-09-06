# uxp

## What it answers

Which agent CLIs are installed, what each can do, and what native argv one
normalized `Invocation` becomes on each of them. Session-store contents are
read elsewhere; `uxp` only resolves the paths.

## Use it when

- discover installed agent CLIs and their capabilities → `Adapter.Detect()`, `Adapter.Capabilities()`
- locate a CLI's session store → `StorePaths`, `ProjectKeyStrategy`
- turn a normalized request into argv → `invoke.Build(Invocation)`
- explain degradation before running → the per-option `MappingSupport` (native, shim, unsupported, dangerous)
- compare built-in agent tools across vendors → the `ToolCapability` taxonomy (Bash to `shell.exec`, Read to `file.read`)

## Quick start

```go
spec, diags, err := invoke.Build(invoke.Invocation{
    CLI:    "codex",
    Mode:   invoke.ModeRun,
    Prompt: "summarise the diff",
})
```

## Contract

- The parity matrices in the reference page are generated from each adapter's `Mappings()` by `go/core/uxp/internal/parityreadme`. Run `go generate ./go/core/uxp/...`; CI fails on a non-empty diff. Edit `Mappings()`, never the table.
- Anti-shims are refusals, not degradations: `ApprovalAutoEdit` never falls back to a target's auto-all flag, `Fork` never emulates via resume plus a fresh session, and `Sandbox*` never cross-shims to container isolation.
- Shims are a closed set of six, fixed in spec section 15.5. Adapters do not invent new ones.
- `Config["uxp.allow_dangerous"]` (bool) is required before any `MappingDangerous` mapping emits, for example `--yolo` or `--dangerously-skip-permissions`.
- `Config["uxp.shim.dir_to_files_max"]` (int, default 200) caps S-2 enumeration; overflow is a hard error.
- Per-adapter keys use the `<cli>.<key>` namespace; only `uxp.*` is cross-adapter.
- Detection-only entries (amp, antigravity, tabnine, windsurf) have no invocation adapter; `invoke.Build` on one returns a hard error naming the right path.

## Neighbours

- [`invoke/adapters/`](invoke/adapters/): one package per CLI, each with its own README for flag mapping, shims, refusals and `Config` keys.
- [`invoke/shim/`](invoke/shim/): the six shim helpers.
- `go/core/uxp/internal/parityreadme`: the matrix generator.

## See also

- [UXP reference](../../../docs/adopters/reference/uxp.md): universal-option and tool-capability parity matrices, shim catalog, detection-only CLIs, universal and per-adapter `Config` keys
- [Go primitives index](../../../docs/adopters/reference/go-primitives.md)
