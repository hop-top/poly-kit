# Command Reflection Reference

> Reference for `hop.top/kit/go/ai/cmdreflect`: one cobra tree,
> reflected once. `Reflect(root)` returns a `Tree` holding one
> `Descriptor` per command, and every consumer projects from that
> instead of walking.

## Why

Kit derives a lot from an adopter's cobra tree: the `<tool> spec`
manifest, the neutral `ToolSpec` that feeds MCP and OpenAPI adapters,
and the leaf set every `cmdsurface` transport mounts. Each of those
used to walk the tree itself, and each dropped a different subset of
commands on the way — one skipped hidden commands, another skipped
deprecated ones, a third skipped non-runnable groups — with no record
of what was skipped or why. Divergence stayed invisible until a
command turned up on one surface and not another.

`cmdreflect` is the single reflector. `Reflect(root)` returns a `Tree`
holding one `Descriptor` per command, and every consumer projects from
that instead of walking.

## The one-descriptor rule

**Every command in the tree is reflected. Nothing is silently
dropped.** A command a consumer must not invoke is still described,
with `Invocable` false and a `NonInvocableReason` naming the rule that
excluded it. Filtering is a consumer decision applied to a complete
reflection, never a walker decision applied to an incomplete one.

```go
tree := cmdreflect.Reflect(root, cmdreflect.WithReserved(kitRoot))

for _, d := range tree.Invocable() {
    mount(d)
}
for _, d := range tree.NonInvocable() {
    log.Printf("%s withheld: %s", d.PathKey(), d.Reason.Explain())
}
```

## Consumers

| Consumer | Reads |
|----------|-------|
| Spec generation (`go/ai/toolspec/cli`) | the whole tree, projected into `ToolSpec` and `Manifest` |
| MCP (`go/ai/toolspec/adapters`) | the `ToolSpec` the tree produced |
| OpenAPI | `Path`, `Args`, `Flags`, `Output.Schema` |
| Command transports (`go/transport/cmdsurface`) | `Tree.Invocable()` for leaves, `Safety` for the policy gate |

## Non-invocable reasons

| Reason | Meaning |
|--------|---------|
| `not-runnable` | a command group: has subcommands, no action of its own |
| `builtin` | cobra/fang framework command (`help`, `completion`, `man`, `__complete`) |
| `hidden-internal` | `Hidden` is set: not part of the supported surface |
| `deprecated` | carries a deprecation marker; withheld from projected surfaces |
| `interactive` | `kit/side-effect=interactive`: needs a terminal and a human |
| `unauthorized-destructive` | destructive and not authorized on this surface |
| `management-only` | reserved to the tool's own management surface (e.g. `spec`) |
| `self-hosting` | hosts or modifies the tool itself; runs from the CLI only (see below) |
| `malformed-schema` | declared metadata does not resolve |

Exactly one reason is recorded per command. When several rules would
fire the most specific wins, so the answer to "why can't I call this?"
does not depend on evaluation order inside the walker. Structural
facts (`not-runnable`, `builtin`) outrank declaration defects, which
outrank surface withdrawal (`hidden-internal`, `self-hosting`,
`management-only`, `deprecated`), which outranks behavioral exclusion
(`interactive`, `unauthorized-destructive`).

### Self-hosting

A command is self-hosting when running it from inside a served
invocation would start a server inside the server, or replace the
binary that is serving. Any one of three signals marks it:

| Signal | Example |
|--------|---------|
| it is named `serve`, or sits under one, at any depth | `mytool serve`, `mytool serve socket`, `mytool svc serve` |
| it declares `kit/network: ingress` | a `listen` command that accepts connections |
| it carries `kit/self-hosting: true` | an `upgrade` that replaces the binary |

None of the three consults the reserved-verb lookup, so a bare cobra
tree withholds its server too. **No option lifts `self-hosting`**:
the relaxations below exist so a consumer can reflect for the surface
it has — a terminal lifts `interactive`, the spec lifts
`management-only` — and no projected surface ever has "is the process
itself". Kit's own `serve` is both reserved and self-hosting; it
reports `self-hosting`, the answer that says why calling it through a
transport can never work.

Mark your own self-modifying commands with the annotation:

```go
cmd.Annotations[cmdmeta.KeySelfHosting] = "true"
```

## Options

Reasons are surface-relative: the local CLI can run an interactive
command, a REST transport cannot. Options lift individual exclusions
so each consumer reflects for its own surface.

| Option | Effect |
|--------|--------|
| `WithReserved(r)` | supplies the reserved-verb lookup (normally the kit `Root`); without it nothing is `management-only` |
| `AllowHidden()` | hidden commands become invocable |
| `AllowDeprecated()` | deprecated commands become invocable |
| `AllowInteractive()` | interactive commands become invocable |
| `AllowReserved()` | kit-reserved management verbs become invocable |
| `DenyDestructive()` | destructive commands become non-invocable with `unauthorized-destructive` |
| `DenyDestructiveFunc(fn)` | denies destructive commands selectively |

## Descriptor shape

Three groups of facts:

- **Identity** — `Path`, `Use`, `Short`, `Long`, `Aliases`.
- **Interface** — `Flags` (type, default, required, hidden,
  deprecated, since-version), `Args`, `Output` (schema, version,
  examples, next steps).
- **Governance** — `Safety` (tier, permission tokens, confirmation,
  auth, idempotency, exit codes), `Surface` (hidden, deprecated,
  reserved, transport annotations), and `Invocable` / `Reason`.

`Safety` reuses the vocabulary
[`go/ai/toolspec`](toolspec-api.md#safety-vocabulary) already defines:
the six-tier side-effect ladder and the `kit:` permission tokens. This
package resolves annotations into that vocabulary; it does not define a
competing one.

### Two confirmation fields

`Safety.RequiresConfirmation` is the resolved verdict — will a human
be asked. `Safety.ConfirmationDeclared` is the narrow fact — did the
adopter set `kit/requires-confirmation`. They differ because a
transport that already gates destructiveness through its own policy
needs to know whether the adopter asked for a confirmation *channel*
on top of that, which is a different question from whether the command
is dangerous.

## Reflect a completed tree

`Reflect` expects a fully assembled root: every subcommand registered,
every flag declared. Reflecting a tree still under construction yields
descriptors that do not match what the binary exposes.

## Describe one command

`Describe(root, cmd)` returns the descriptor `Reflect` would record
for `cmd` — same facts, same verdict — without walking the tree. It
is for a consumer that already holds a resolved `*cobra.Command` and
needs its tier, output schema, or self-hosting status at the moment
of use; the command runners in
[`go/transport/cmdsurface`](../../../go/transport/cmdsurface/README.md)
call it before executing a leaf.

## Related pages

- [`toolspec-api.md`](toolspec-api.md): the `ToolSpec` and `Manifest` shapes projected from descriptors, and the safety vocabulary
- [`../integrations/toolspec-adopter-guide.md`](../integrations/toolspec-adopter-guide.md): publishing the manifest the reflector feeds
- [`../guides/expose-cli-over-mcp.md`](../guides/expose-cli-over-mcp.md), [`../guides/expose-cli-over-rest.md`](../guides/expose-cli-over-rest.md): transports that mount `Tree.Invocable()`
- [`go-primitives.md`](go-primitives.md): where `cmdreflect` sits among the Go primitives
