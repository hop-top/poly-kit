# cmdreflect

## What it answers

What every command in a cobra tree is, and for the ones a surface must
not call, why: one `Descriptor` per command, nothing silently dropped.
The single reflector behind `<tool> spec`, MCP, OpenAPI and the
`cmdsurface` transports. Wrong package when you need the projected
`ToolSpec`/`Manifest` shapes (`hop.top/kit/go/ai/toolspec/cli`) or the
safety vocabulary itself (`hop.top/kit/go/ai/toolspec`).

## Use it when

- you mount leaves on a transport → `cmdreflect.Reflect(root, cmdreflect.WithReserved(kitRoot))` then `tree.Invocable()`
- you need to explain a withheld command → `tree.NonInvocable()` and `d.Reason.Explain()`
- your surface can run what a transport cannot → `AllowHidden()`, `AllowDeprecated()`, `AllowInteractive()`, `AllowReserved()`
- your surface must refuse destructive commands → `DenyDestructive()` or `DenyDestructiveFunc(fn)`
- you already hold a resolved `*cobra.Command` → `cmdreflect.Describe(root, cmd)` for its tier, output schema or self-hosting status
- a command replaces the binary or serves → `cmd.Annotations[cmdmeta.KeySelfHosting] = "true"`

## Quick start

```go
tree := cmdreflect.Reflect(root, cmdreflect.WithReserved(kitRoot))

for _, d := range tree.Invocable() {
    mount(d)
}
for _, d := range tree.NonInvocable() {
    log.Printf("%s withheld: %s", d.PathKey(), d.Reason.Explain())
}
```

## Contract

- One-descriptor rule: every command is reflected; a withheld one carries
  `Invocable == false` and exactly one `NonInvocableReason`
  (`not-runnable`, `builtin`, `hidden-internal`, `deprecated`,
  `interactive`, `unauthorized-destructive`, `management-only`,
  `self-hosting`, `malformed-schema`). Most specific reason wins:
  structural, then declaration defects, then surface withdrawal, then
  behavioral exclusion.
- No option lifts `self-hosting` (named `serve` or under one,
  `kit/network: ingress`, or `kit/self-hosting: true`).
- Without `WithReserved` nothing is `management-only`.
- `Reflect` expects a fully assembled root; reflecting a tree under
  construction yields descriptors that do not match the binary.
- `Safety.RequiresConfirmation` is the resolved verdict;
  `Safety.ConfirmationDeclared` is whether `kit/requires-confirmation`
  was set.
- `Safety` reuses the `toolspec` six-tier ladder and `kit:` permission
  tokens; this package defines no competing vocabulary.
- A command with no `kit/side-effect` resolves to `TierUnannotated`, not
  `TierRead`, unless the destructive-name heuristic fires (`TierInferred`
  marks that case). Check `Tier.Declared()` before treating a tier as the
  adopter's word. Unannotated projects conservatively — `Caution`,
  `kit:fs:write:local` — and stays invocable, because withholding it
  would break working CLIs to punish missing metadata.
- A `kit/side-effect` value that does not resolve is `TierUnknown` and a
  declaration defect: the command is described and withheld with
  `malformed-schema`, so a typo surfaces instead of shipping.

## Neighbours

- `hop.top/kit/go/ai/toolspec` and `toolspec/cli`: vocabulary and the
  `ToolSpec`/`Manifest` projections
- `hop.top/kit/go/ai/toolspec/adapters`: MCP renderer over the projected spec
- [`hop.top/kit/go/transport/cmdsurface`](../../transport/cmdsurface/README.md):
  transports mounting `Tree.Invocable()`, calling `Describe` before a leaf runs

## See also

- [Command reflection reference](../../../docs/adopters/reference/cmdreflect.md):
  reasons table, precedence, self-hosting signals, options, descriptor shape
- [ToolSpec API reference](../../../docs/adopters/reference/toolspec-api.md)
