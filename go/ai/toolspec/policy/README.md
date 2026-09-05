# policy

## What it answers

Given a command's side-effect class and network axis, should a harness
auto-allow, prompt, or deny? Wrong package when you need to read those
annotations off a cobra tree (`hop.top/kit/go/ai/toolspec/cli`) or to gate
an MCP request end to end (`hop.top/kit/go/ai/toolspec/adapters`).

## Use it when

- you need kit's default table: `policy.Default()`
- an adopter ships an overlay file: `policy.LoadOrDefault(path)` (overlay rules win on collisions)
- you parse a table from bytes or a reader: `policy.LoadBytes`, `policy.Load`, `policy.LoadFromFile`
- you combine two tables yourself: `policy.Merge(base, overlay)`
- you resolve one tuple: `table.Resolve(sideEffect, network)` returns a `Decision` with `Action`, `Reason`, `Source`

## Quick start

```go
table := policy.Default()
read := table.Resolve(policy.SideEffectRead, policy.NetworkNone)
push := table.Resolve(policy.SideEffectDestructive, policy.NetworkEgress)
fmt.Println(read.Action)
fmt.Println(push.Action)
// Output:
// auto-allow
// deny
```

Verified by `example_test.go` in this directory.

## Contract

- Resolution order: exact `(side_effect, network)` match, then `(side_effect, any)`, then `prompt` with a "no rule matched" reason. Unmapped tuples are never auto-allowed.
- Duplicate keys: first rule wins, so overlays prepend.
- Resolution is deterministic and stateless; `Default()` returns a fresh copy each call.
- Vocabulary: side effects `read | write | destructive | interactive`; network `none | local-only | egress | any`; actions `auto-allow | prompt | deny`.
- `default.yaml` in this directory is the embedded authority; `schema_version` locks its layout.

## Neighbours

- `hop.top/kit/go/ai/toolspec/adapters`: `EnforceMCPRequest` wraps `Resolve` into an MCP error envelope.
- `hop.top/kit/go/ai/toolspec`: the `Permission` tokens the six-tier ladder projects; this table still keys on the four legacy classes.
- `hop.top/kit/go/console/cli`: the `SideEffect` enum this package mirrors to avoid an import cycle.

## See also

- [Consume kit's toolspec contract from your harness](../../../../docs/adopters/integrations/toolspec-harness-guide.md)
- [Claude Code permissions](../../../../docs/adopters/integrations/claude-code-permissions.md)
- [Choose an enforcement mode](../../../../docs/adopters/guides/choose-enforcement-mode.md)
