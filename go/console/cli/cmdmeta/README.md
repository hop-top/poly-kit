# cmdmeta

## What it answers

"What do the `kit/*` annotations on this cobra command say?" without
importing `hop.top/kit/go/console/cli`. Read-only leaf package: cobra
and the standard library only. To declare annotations (SetSideEffect,
SetOutputSchema, SetExamples, ...) use `hop.top/kit/go/console/cli`.

## Use it when

- reflect a command tree from a transport or reflection layer → `cmdmeta.Is*`, `cmdmeta.Get*`
- decide whether `--dry-run` is honored for a leaf → `cmdmeta.IsDryRunSupported(cmd)`
- render examples or follow-up suggestions for agents → `cmdmeta.GetExamples`, `cmdmeta.GetNextSteps`
- pull the declared output schema and its version → `cmdmeta.GetOutputSchemaJSON(cmd)`
- check a boolean marker by key → `cmdmeta.ReadBool(cmd, cmdmeta.KeyPassthrough)`
- write your own reader for a `kit/*` key → the `Key*` constants

## Quick start

```go
cmd := &cobra.Command{
    Use: "deploy",
    Annotations: map[string]string{
        cmdmeta.KeySideEffect: "write",
        cmdmeta.KeyRetryable:  "true",
        cmdmeta.KeyNextSteps:  `[{"when":"on success","suggest":"kit status","reason":"confirm rollout"}]`,
    },
}

fmt.Println("retryable:", cmdmeta.IsRetryable(cmd))
fmt.Println("dry-run:", cmdmeta.IsDryRunSupported(cmd))
steps, ok := cmdmeta.GetNextSteps(cmd)
fmt.Println(ok, steps[0].Suggest, "/", steps[0].Reason)
```

## Contract

- Import graph: cmdmeta imports nothing from `hop.top/kit`. Keeping it a
  leaf is what lets `hop.top/kit/go/ai/cmdreflect` and transports reflect
  a command tree without the `transport/api → cmdreflect → cli` cycle.
- Boolean markers (`top-level-verb`, `hierarchical`, `passthrough`,
  `retryable`, `self-hosting`) read true only when the value is the
  string `"true"`. Nil command or nil annotation map reads false.
- `IsDryRunSupported` resolution order: `kit/dry-run: opted-out` is
  false; `kit/dry-run: supported` is true; side-effect `write*` or
  `destructive*` is true; `read`, `interactive`, unknown or missing tag
  is false.
- `GetExamples` and `GetNextSteps` return `(nil, false)` on absent or
  malformed JSON; decode errors are swallowed.
- `GetOutputSchemaJSON` returns the raw bytes unvalidated, with the
  `kit/output-schema-version` string alongside.
- Every reader stays reachable under its original `cli.*` spelling;
  `cli` forwards to this package.

## Neighbours

- `hop.top/kit/go/console/cli`: the setters, SideEffect type, and
  validation against cli-owned state.
- `hop.top/kit/go/ai/cmdreflect`: reflection over a command tree, the
  primary consumer of these readers.
- `hop.top/kit/go/transport/api`: HTTP surface that reflects the tree
  it serves.

## See also

- [docs/adopters/reference/go-primitives.md](../../../../docs/adopters/reference/go-primitives.md)
