# toolspec

## What it answers

What a CLI tool can do and how risky each command is, as data: a
recursive command tree plus safety metadata, the shape behind `<tool>
spec` and the adapter targets (MCP, kit-manifest, OpenAPI). Adopters
declare behavior with cobra annotations; `cmdreflect` reflects them and
this package's projections carry them. Wrong package when you need the
reflector (`hop.top/kit/go/ai/cmdreflect`), the `spec` subcommand
(`cli/`), a wire format (`adapters/`) or the allow/prompt/deny table
(`policy/`).

## Use it when

- you annotate a command's risk → `kit/side-effect` (six-tier ladder), `kit/network`, `kit/idempotent`, `kit/exec`, `kit/bus-publish`
- you read or build a spec in Go → `ToolSpec`, `Command`, `Contract`, `Safety`, `Permission`
- you resolve a spec for a third-party tool → `toolspec.NewRegistry(toolspec.WithSource(...), toolspec.WithCache(store))` then `reg.Resolve(name)`
- you advertise a running service's capabilities → `toolspec.NewCapabilitySet(name, version)`, `cs.Add(...)`, `cs.JSON()`

## Quick start

```go
cmd.Annotations = map[string]string{
    "kit/side-effect": "destructive-shared",
    "kit/network":     "egress:public",
    "kit/idempotent":  "no",
}
```

## Contract

- Reflection lives in `cmdreflect`: `WalkCobra` and `BuildManifest` are
  projections from its descriptors; the `NonInvocableReason` vocabulary
  is defined there, not here.
- `kit/side-effect` accepts `read`, `write-local`, `write-shared`,
  `destructive-local`, `destructive-shared`, `interactive`; legacy `write`
  and `destructive` map conservatively to shared scope.
- `kit/network` defaults to `none` and is orthogonal to the tier;
  `egress:private` and `ingress` force `RequiresConfirmation=true`.
- Each value projects one `kit:` permission token into
  `Safety.Permissions`; the harness default-policy table decodes
  tier × network into `auto` / `prompt` / `deny`:
  [Safety vocabulary](../../../docs/adopters/reference/toolspec-api.md#safety-vocabulary).
- Legacy 4-tier annotations keep working; migration steps:
  [Migrate to the six-tier ladder](../../../docs/adopters/integrations/toolspec-adopter-guide.md#migrate-to-the-six-tier-ladder).

## Neighbours

- [`adapters/`](adapters/README.md): `FormatAdapter` implementations (kit-manifest, mcp) and `EnforceMCPRequest`
- [`cli/`](cli/README.md): `<tool> spec` subcommand, `WalkCobra`, `BuildManifest`
- [`policy/`](policy/README.md): tier × network → allow/prompt/deny table
- [`sources/`](sources/README.md): ingest sources (help, completion, tldr, thefuck, llm, usp)
- `hop.top/kit/go/ai/cmdreflect`: the single cobra reflector

## See also

- [ToolSpec API reference](../../../docs/adopters/reference/toolspec-api.md):
  types, safety vocabulary, permissions, registry, capabilities
- [Publish your kit-powered CLI's manifest](../../../docs/adopters/integrations/toolspec-adopter-guide.md)
- [Consume the contract from a harness](../../../docs/adopters/integrations/toolspec-harness-guide.md)
