# adapters

## What it answers

How a `toolspec.ToolSpec` becomes bytes in a named machine-readable format
(`<tool> spec --format <name>`), plus the MCP-side policy gate. Wrong package
when you need to build the spec from a cobra tree
(`hop.top/kit/go/ai/toolspec/cli`) or to decide policy outcomes
(`hop.top/kit/go/ai/toolspec/policy`).

## Use it when

- you render the kit manifest or an MCP tool listing yourself: `adapters.KitManifest()`, `adapters.MCP()`, then `Render(w, spec, opts...)`
- you add a new output format: implement `adapters.FormatAdapter` and pass it to `cli.WithFormatAdapter`
- you need name or alias lookup outside `RegisterSpecCommand`: `adapters.NewRegistry()`, `Register`, `Lookup`, `Names`, `Default`
- an MCP host must gate a tool call against the manifest: `adapters.EnforceMCPRequest(manifest, path, table)`

## Quick start

```go
spec := &toolspec.ToolSpec{
    Name:          "demo",
    SchemaVersion: "1.0",
    Commands:      []toolspec.Command{{Name: "list"}},
}
if err := adapters.KitManifest().Render(os.Stdout, spec); err != nil {
    panic(err)
}
// Output:
// {
//   "tool": "demo",
//   "version": "",
//   "schema_version": "1.0",
//   "commands": [
//     {
//       "path": [
//         "demo",
//         "list"
//       ],
//       "short": "",
//       "side_effect": "",
//       "idempotent": "",
//       "retryable": false,
//       "dry_run_supported": false
//     }
//   ]
// }
```

Verified by `example_test.go` in this directory.

## Contract

- Adapters are stateless; `Render` may run concurrently on one instance. Per-render knobs are `RenderOption` values (`WithPretty`, `WithSchemaVersion`, `WithIncludeDeprecated`, `WithCustom`); unknown options are ignored.
- `Name()` is lowercase hyphenated ASCII; names and aliases share one namespace per registry and collide at `Register` time.
- Custom keys: `kit-manifest:output-format` (json, yaml, table; default json), `kit-manifest:prebuilt` (a `toolspec.Manifest` value used verbatim), `mcp:description`, `mcp:required-flags` (`[]string`).
- `EnforceMCPRequest` is pure. A path missing from the manifest resolves to deny; a deny carries `MCPError` with code `MCPErrorCodePolicyDeny` (-32099).

## Neighbours

- `hop.top/kit/go/ai/toolspec/cli`: owns the canonical registry and the `--format` dispatch.
- `hop.top/kit/go/ai/toolspec/policy`: the `Table` and `Decision` the enforcement gate consumes.
- `hop.top/kit/go/console/output`: the renderer behind the kit-manifest json, yaml and table formats.

## See also

- [Consume kit's toolspec contract from your harness](../../../../docs/adopters/integrations/toolspec-harness-guide.md)
- [Expose a CLI over MCP](../../../../docs/adopters/guides/expose-cli-over-mcp.md)
- [Claude Code permissions](../../../../docs/adopters/integrations/claude-code-permissions.md)
