# cli

## What it answers

How a kit-powered CLI publishes its own capability manifest: the `<tool> spec`
subcommand and the walker that projects a live cobra tree into
`toolspec.Manifest` and `toolspec.ToolSpec`. Wrong package when you need to
render the manifest in a specific wire format (`hop.top/kit/go/ai/toolspec/adapters`)
or to reflect a single command (`hop.top/kit/go/ai/cmdreflect`).

## Use it when

- your tool should answer `<tool> spec` for agents: `cli.RegisterSpecCommand(root, "1.0")` once, after every command is registered
- you need the manifest in-process (tests, bootstrap commands): `cli.EmitManifest(root, "1.0")`
- you want deprecated leaves included: `cli.BuildManifest(root, "1.0", true)`
- you need the tree-shaped `ToolSpec` instead of the flat manifest: `cli.WalkCobra(root.Cmd)`
- you ship extra formats or curated error patterns: `cli.WithFormatAdapter`, `cli.WithErrorPatterns`, `cli.WithStateIntrospection`, `cli.WithDefaultFormat`

## Quick start

```go
root := kitcli.New(kitcli.Config{Name: "demo", Version: "1.0.0", Short: "Demo tool"})
list := &cobra.Command{Use: "list", Short: "List items", RunE: func(*cobra.Command, []string) error { return nil }}
kitcli.SetSideEffect(list, kitcli.SideEffectRead)
kitcli.SetIdempotency(list, kitcli.IdempotencyYes)
root.Cmd.AddCommand(list)

m := cli.EmitManifest(root, "1.0")
fmt.Println(m.Tool, m.SchemaVersion, len(m.Commands))
fmt.Println(m.Commands[0].Path, m.Commands[0].SideEffect, m.Commands[0].Idempotent)
// Output:
// demo 1.0 1
// [demo list] read yes
```

Verified by `example_test.go` in this directory.

## Contract

- `schemaVersion` is the CLI schema version (MAJOR.MINOR), distinct from the binary semver in `root.Config.Version`.
- `<tool> spec` is tagged `kit/side-effect=read`, `kit/idempotent=yes` and carries `kit/spec-command`; it passes `Root.Validate` and skips the deprecation-warning middleware.
- Flags: `--version` prints only the schema version; `--include-deprecated` opts deprecated leaves back in (default hidden); `--format` selects an adapter, `--format-help` lists them.
- Reflection is delegated to `cmdreflect`; this package only projects descriptors and applies the manifest inclusion policy.
- `RegisterSpecCommand` returns an error only on adapter name or alias collisions; the subcommand still mounts with the adapters that registered.
- Curation fields (`ErrorPatterns`, `Workflows`, `StateIntrospection`) are never derived from cobra; they come from `RegisterOption` values.

## Neighbours

- `hop.top/kit/go/ai/toolspec`: the `ToolSpec` and `Manifest` data types and safety vocabulary.
- `hop.top/kit/go/ai/toolspec/adapters`: `FormatAdapter` implementations (kit-manifest, mcp) and the adapter registry.
- `hop.top/kit/go/ai/toolspec/policy`: the permission table harnesses apply to a manifest.
- `hop.top/kit/go/ai/cmdreflect`: the single cobra reflector and the `NonInvocableReason` vocabulary.
- `hop.top/kit/go/console/cli`: `Root`, `SetSideEffect`, `SetIdempotency`.

## See also

- [Publish your kit-powered CLI's manifest](../../../../docs/adopters/integrations/toolspec-adopter-guide.md)
- [Expose a CLI over MCP](../../../../docs/adopters/guides/expose-cli-over-mcp.md)
- [ToolSpec API reference](../../../../docs/adopters/reference/toolspec-api.md)
