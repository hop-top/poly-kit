# badge

## What it answers

How the `kit conformance badge` leaf writes the shields.io endpoint JSON
(`.12fc.json`) from a per-factor matrix, or seeds an ungradable one. Verdict
rules and the JSON encoder live in `hop.top/kit/go/conformance/badge`; import
that for custom regen pipelines.

## Use it when

- scaffolding a project → `kit conformance badge --emit-seed`
- regenerating after a conformance run → `kit conformance badge --matrix=docs/12-factor-matrix.json`
- writing elsewhere than `./.12fc.json` → `-o` / `--output`
- mounting on a custom root → `root.AddCommand(badge.Cmd())`

## Quick start

```go
out := filepath.Join(os.TempDir(), "example-12fc.json")
defer os.Remove(out)

cmd := badge.Cmd()
cmd.SetArgs([]string{"--emit-seed", "--output", out})
cmd.SetOut(os.Stderr)
if err := cmd.Execute(); err != nil {
    fmt.Println("error:", err)
    return
}

data, _ := os.ReadFile(out)
fmt.Print(string(data))
// {
//   "schemaVersion": 1,
//   "label": "12-factor AI-CLI",
//   "message": "ungradable",
//   "color": "lightgrey",
//   "labelColor": "555",
//   "namedLogo": "robotframework",
//   "cacheSeconds": 300
// }
```

## Contract

- `--emit-seed` and `--matrix` are mutually exclusive; one is required
- Matrix file: `{"schemaVersion": 1, "factors": [{"n", "name", "tier",
  "status", "evidence"}]}` with 12 entries; `tier` is `must|should|may`,
  `status` is `pass|fail|skip`
- Output is deterministic (stable field order, two-space indent, trailing
  newline); an invalid matrix degrades to the lightgrey `ungradable` badge
- Never touches the network
- Annotations: side effect `write-local`, idempotent `yes`

## Neighbours

- `hop.top/kit/go/conformance/badge`: `Validate`, `Verdict`, `WriteJSON`
- `hop.top/kit/go/console/cli/conformance`: mounts this leaf

## See also

- [kit init guide](../../../../../docs/adopters/guides/kit-init.md)
