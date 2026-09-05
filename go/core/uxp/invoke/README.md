# invoke

## What it answers

How to turn one normalized `Invocation` into native argv for a given
agent CLI, with every shim or refusal named before anything runs. Which
CLIs are installed, and where their session stores live, is `go/core/uxp`;
describing your own CLI to an agent is `go/ai/toolspec`.

## Use it when

- you launch an agent CLI from a tool: pick the adapter under `adapters/<cli>`, call `Build(inv)`, exec the `CommandSpec`
- you must explain degradation to the user first: inspect `Diagnostics` (`HasErrors`, `Errors`, `Filter(level)`)
- you want a one-shot build-and-exec: implement or wire a `Runner`
- you render a parity table: `Mappings()` and `ToolCapabilities()` on any adapter
- you drive it from the shell: `kit uxp explain | capabilities | tools | run` in `cmd/uxp`

## Quick start

```go
spec, ds, err := claude.New().Build(invoke.Invocation{
    CLI:    uxp.CLIClaude,
    Mode:   invoke.ModeRun,
    Prompt: "summarize this repo",
})
if err != nil || ds.HasErrors() {
    fmt.Println("refused:", err, ds.Errors())
    return
}
fmt.Println(spec.Path, spec.Args)
// Output: claude [-p summarize this repo]
```

## Contract

- `Build` is pure: no exec, no filesystem writes. `CommandSpec.Args` excludes `Path`, matching `os/exec.Cmd.Args[1:]`.
- Every universal option maps as `native`, `shim`, `unsupported` or `dangerous`. Shims emit a warning diagnostic; unsupported options requested by the caller make `Build` return an error; dangerous mappings are refused unless `Config["uxp.allow_dangerous"] = "true"`.
- Anti-shims: `ApprovalAutoEdit` never degrades to a target's auto-all flag, `Fork` is never emulated by resume plus fresh session, `Sandbox*` never cross-shims to container isolation.
- `ModeResume` needs `SessionID` or `Continue`. `Config` keys are `<cli>.<key>` for one adapter and `uxp.<key>` across adapters; unknown keys yield an info diagnostic.
- `OutputJSON` is one final-message object; `OutputStreamJSON` is the CLI's native event stream.

## Neighbours

- [`adapters/`](adapters/README.md): one package per CLI.
- [`shim/`](shim/README.md): the closed catalog of mapping helpers adapters share.
- `cmd/uxp`: the `kit uxp` subcommand tree over `Build`.
- `go/core/uxp`: detection, capabilities, session-store paths.
- `go/ai/toolspec`: permission tokens for a CLI's own command tree.

## See also

- [uxp/README.md](../README.md), universal-option parity matrix
