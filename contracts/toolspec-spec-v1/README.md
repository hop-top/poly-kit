# toolspec-spec-v1

## What it answers

The `kit/output-schema` JSON Schemas the `<tool> spec` and
`<tool> spec coverage` commands carry, as Go reflects them from
`toolspec.Manifest` and `cli.CoverageReport`. SDKs without Go's
reflection embed these bytes so their own `spec` entries match Go's.

## Contract

| File | Holds |
|------|-------|
| `manifest.schema.json` | schema of the `spec` output (`toolspec.Manifest`) |
| `coverage-report.schema.json` | schema of the `spec coverage` output (`CoverageReport`) |

Generated, never hand-edited:

```sh
go test ./go/ai/toolspec/cli/ -run TestSpecOutputSchemaContract \
    -update-spec-schema-contract
```

| Port | Guard |
|------|-------|
| Go | `go/ai/toolspec/cli/spec_schema_contract_test.go` (drift gate) |
| TypeScript | `sdk/ts/src/toolspec-spec.test.ts` (embedded copy in `sdk/ts/src/toolspec-spec-schemas.ts`) |

A regenerated file turns the TS guard red until
`toolspec-spec-schemas.ts` carries the new bytes.

## See also

- [Parity contracts reference](../../docs/adopters/reference/parity-contract.md)
- [ToolSpec API reference](../../docs/adopters/reference/toolspec-api.md)
