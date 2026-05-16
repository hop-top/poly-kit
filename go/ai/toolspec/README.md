# toolspec

Semantic tool definitions and mapping for kit-projected CLI surfaces.

`toolspec` is the data layer behind `<tool> spec` subcommands and the
adapter targets (MCP, kit-manifest, OpenAPI, …). It models a CLI tool
as a recursive command tree plus risk metadata; adopters declare
behaviour with cobra annotations and the walker projects them.

## Safety vocabulary

The walker projects three orthogonal axes from cobra annotations into
`Safety.Level`, `Safety.RequiresConfirmation`, and a typed
`Safety.Permissions` slice. See ADR-0021 for the full design.

### `kit/side-effect` (tier ladder)

Six values, each accepted by the walker. The legacy 4-tier values
keep working:

| Value                   | `Safety.Level` | Permission                   |
|-------------------------|----------------|------------------------------|
| `read`                  | safe           | `kit:fs:read`                |
| `write-local`           | caution        | `kit:fs:write:local`         |
| `write-shared`          | caution        | `kit:fs:write:shared`        |
| `destructive-local`     | dangerous      | `kit:fs:destructive:local`   |
| `destructive-shared`    | dangerous      | `kit:fs:destructive:shared`  |
| `interactive`           | caution        | `kit:fs:read`                |
| `write` (legacy)        | caution        | `kit:fs:write:shared` *      |
| `destructive` (legacy)  | dangerous      | `kit:fs:destructive:shared` *|

\* Legacy values map conservatively: bare `write` and `destructive`
assume shared scope. Adopters who mean `write-local` /
`destructive-local` must declare the value explicitly.

### `kit/network` (orthogonal axis)

Default `none`. Independent of the tier — a `read` can be
`egress:public`; a `destructive-local` can be `none`.

| Value             | Permission                   |
|-------------------|------------------------------|
| (absent or `none`)| `kit:network:none`           |
| `egress:public`   | `kit:network:egress:public`  |
| `egress:private`  | `kit:network:egress:private` |
| `ingress`         | `kit:network:ingress`        |

`egress:private` and `ingress` set `RequiresConfirmation=true`
regardless of the tier.

### Forward-looking capability annotations

| Annotation        | Permission             | Meaning                                   |
|-------------------|------------------------|-------------------------------------------|
| `kit/exec`        | `kit:exec:subprocess`  | Spawns a subprocess.                      |
| `kit/bus-publish` | `kit:bus:publish`      | Publishes events to the kit event bus.    |

Both are emitted only when the corresponding annotation is set
(any non-empty value is accepted).

### Default-policy table (harness reference)

This is the recommended decoder for `Safety.Permissions`; the
canonical contract lives in the `kit-toolspec-ai-harness-contract`
track. `auto` = allow without prompt; `prompt` = ask user;
`deny` = refuse unless explicitly allowlisted.

| Tier × Network        | none    | egress:public | egress:private | ingress |
|-----------------------|---------|---------------|----------------|---------|
| `read`                | auto    | auto          | prompt         | prompt  |
| `write-local`         | auto    | auto          | prompt         | prompt  |
| `write-shared`        | prompt  | prompt        | prompt         | prompt  |
| `destructive-local`   | prompt  | prompt        | prompt         | prompt  |
| `destructive-shared`  | prompt  | prompt        | deny           | deny    |
| `interactive`         | prompt  | prompt        | prompt         | prompt  |

## Adopter migration

Most adopters need no change. Existing `kit/side-effect` values
(`read|write|destructive|interactive`) keep working; the walker
maps them into the expanded ladder when projecting permissions.

Tighten where you want the harness to relax:

- A `write` that mutates only CWD-local state → switch to
  `write-local`. The harness will auto-allow it instead of
  prompting.
- A `destructive` that targets only CWD-local state → switch to
  `destructive-local`. The harness will prompt (still) but won't
  treat egress-private as a hard deny.

Tighten where you want the harness to escalate:

- A `read` that hits an internal control-plane → add
  `kit/network: egress:private`. The harness will switch from
  auto-allow to prompt.
- A long-running `serve` → declare `kit/network: ingress`.

Annotations work as cobra `Annotations` map entries. Example:

```go
cmd.Annotations = map[string]string{
    "kit/side-effect": "destructive-shared",
    "kit/network":     "egress:public",
    "kit/idempotent":  "no",
}
```

## Core types

- [`ToolSpec`](spec.go): full spec for a CLI tool (commands,
  flags, errors, workflows).
- [`Command`](spec.go): command tree node with safety, contract,
  output schema, intent.
- [`SafetyLevel`](spec.go): legacy 3-value enum
  (`safe`/`caution`/`dangerous`).
- [`Permission`](permissions.go): typed permission tokens emitted
  into `Safety.Permissions`.
- [`Contract`](spec.go): idempotency, side effects, pre-conditions.

## Registry

[`Registry`](registry.go) resolves specs from ordered
[`Source`](source.go) implementations with optional caching:

```go
reg := toolspec.NewRegistry(
    toolspec.WithSource(sources.Help{}),
    toolspec.WithCache(store),
)
spec, _ := reg.Resolve(ctx, "kubectl")
```

## Capabilities

[`CapabilitySet`](capabilities.go) describes discoverable
capabilities of a running service:

```go
cs := toolspec.NewCapabilitySet("myapp", "1.0.0")
cs.Add(toolspec.Capability{
    Name: "list-items", Type: "endpoint", Path: "/items",
})
data, _ := cs.JSON()
```
