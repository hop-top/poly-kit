# Publish your kit-powered CLI's manifest

Status: published
Track: `kit-toolspec-ai-harness-contract`
Audience: adopters building a kit-powered CLI who want to expose
their tool's capability manifest to AI harnesses (Claude Code, MCP
hosts, agent frameworks) per ADR-0022.

## TL;DR — one line

If your CLI already calls `cli.New(...)` and registers cobra
subcommands with `kit/side-effect` annotations, you're already
publishing the manifest. Add this once, after registering all
your commands:

```go
cli.RegisterSpecCommand(root, "1.0")
```

Your binary now serves `<your-tool> spec --format kit-manifest`
and harnesses can consume it. That's the whole adoption.

## Three publishing surfaces

Pick whichever matches your tool's voice. They emit the same
payload; choose by ergonomics.

### `<tool> spec`

The default. `cli.RegisterSpecCommand` adds it.

```sh
$ mytool spec --format kit-manifest | head
{
  "tool": "mytool",
  "version": "1.0.0",
  "schema_version": "1.0",
  "commands": [...]
}
```

### `<tool> manifest` alias

The shorter spelling. ADR-0022 prefers this for harness consumption
("agents read manifests, they don't author them"). Add via:

```go
cli.RegisterManifestCommand(root, "1.0")
```

You can register both — they're independent subcommands with
identical behaviour.

### `kit toolspec` (kit binary only)

This is the bootstrap manifest of the kit binary itself. Adopters
don't wire it; it ships when kit ships. Useful for harnesses that
need to confirm the kit-toolspec contract is installed at all.

## Annotate every leaf

Without `kit/side-effect`, the policy gate cannot resolve your
commands and falls back to `prompt` for every call. Annotate every
runnable leaf:

```go
import kitcli "hop.top/kit/go/console/cli"

cmd := &cobra.Command{Use: "list", RunE: ...}
kitcli.SetSideEffect(cmd, kitcli.SideEffectRead)
kitcli.SetIdempotency(cmd, kitcli.IdempotencyYes)
```

Or set annotations directly:

```go
cmd.Annotations = map[string]string{
    "kit/side-effect": "read",
    "kit/idempotent":  "yes",
}
```

The four current side-effect classes (read | write | destructive |
interactive) live in `go/console/cli/sideeffect.go`. The
`kit-toolspec-safety-ladder` track will expand this; today, pick the
closest match.

## Schema versioning

`schema_version` is the contract surface — bump it in lock-step
with the kit `toolspec.Manifest` schema, NOT with your binary's
semver. Semver describes the binary's behaviour; `schema_version`
describes the manifest layout.

Today every kit-powered CLI is on `"1.0"`. When the safety-ladder
track ships `"2.0"` (richer side-effect enum + populated network
axis), upgrade your call:

```go
cli.RegisterSpecCommand(root, "2.0")
```

…after you have annotated all your leaves with the new vocabulary.
Until then, hold at `"1.0"`.

## Optional: curate

Beyond the auto-walked tree, RegisterSpecCommand accepts curation
options to layer richer knowledge on top:

```go
cli.RegisterSpecCommand(root, "1.0",
    cli.WithErrorPatterns([]toolspec.ErrorPattern{...}),
    cli.WithWorkflows([]toolspec.Workflow{...}),
    cli.WithStateIntrospection(&toolspec.StateIntrospection{
        ConfigCommands: []string{"mytool config path"},
        EnvVars:        []string{"MYTOOL_DATA_PATH"},
    }),
)
```

Curation surfaces in the broader `ToolSpec` shape consumed by
adapters that want it (e.g. MCP renderer); the leaf-flat
kit-manifest format ignores curation.

## Optional: register additional format adapters

If you ship to a community that consumes a non-kit format (OpenAPI,
LangChain tools, your-corp-internal-format), publish it:

```go
cli.RegisterSpecCommand(root, "1.0",
    cli.WithFormatAdapter(myCorpAdapter()),
)
```

Then `mytool spec --format my-corp-name` dispatches to your custom
adapter alongside the built-in `kit-manifest` and `mcp` formats.

## Verifying your publish

Run your tool's `spec` command and pipe through the kit policy
inspector:

```sh
$ mytool spec --format kit-manifest > /tmp/manifest.json
$ kit toolspec policy
# (printed table reflects the default policy that will gate every
#  command in your manifest)
```

Or run the round-trip test against your own fixtures (see
`go/ai/toolspec/adapters/integration_test.go` for the pattern).

## Common pitfalls

- **Forgetting to annotate.** `kit/side-effect` missing →
  EnforceMCPRequest fails safe to "destructive" → every command
  prompts. Annotate every leaf.
- **Hand-authored side-effects.** Use the
  `kitcli.SetSideEffect(...)` helper; it mirrors the closed enum
  Root.Validate enforces.
- **Bumping `schema_version` ahead of the kit binary.** Adopters
  shouldn't lead the schema. Upgrade after kit ships the matching
  vocabulary.
- **Two version strings.** Keep `cli.Config.Version` (binary
  semver) and `cli.RegisterSpecCommand`'s schema version
  separate. They evolve at different rates for different reasons.

## See also

- ADR-0022 — the contract
- [harness consumption guide](toolspec-harness-guide.md) — the
  other side of the integration
- [Claude Code worked example](claude-code-permissions.md) — what a
  harness does with your manifest
- `~/.ops/docs/cli-conventions-with-kit.md` §13 — the manifest
  schema lock
