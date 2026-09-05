# record

## What it answers

How the `kit conformance harness record` leaf turns a scenario plus a target
binary into a cassette directory that `kit conformance grade` can upload.
Library callers building their own recording pipeline use
`hop.top/kit/go/conformance/recorder` directly.

## Use it when

- recording from a shell → `kit conformance harness record --scenario <yaml> --binary <path> --out <dir>`
- re-recording over an existing cassette → add `--force`
- mounting on a custom root → `root.AddCommand(record.Group())` (the
  `harness` group, annotated hierarchical) or `record.Cmd()` for the bare leaf

## Quick start

```go
harness := record.Group()
harness.SilenceUsage = true
harness.SilenceErrors = true
harness.SetArgs([]string{"record", "--binary", "./bin/acme", "--out", "./cassette"})

err := harness.Execute()
var cliErr *output.Error
if errors.As(err, &cliErr) {
    fmt.Println(cliErr.Code, cliErr.ExitCode)
    fmt.Println(cliErr.Message)
}
// USAGE 3
// conformance harness record: --scenario is required
```

## Contract

- Required: `--scenario`, `--binary`, `--out`. Optional: `--story`,
  `--workdir` (default fresh temp dir), `--scenario-ref <ns>/<id>[@<version>]`,
  `--binary-version`, `--step-timeout` (2m), `--force`
- Every step runs as a real subprocess; exit code, stdout, stderr and
  duration are captured verbatim. Each subprocess sees `XRR_CASSETTE_DIR`
  and `XRR_MODE=record`
- Output layout: `manifest.yaml`, `story.yaml` (byte-exact copy),
  `steps/<id>/result.json`, `steps/<id>/stdout.txt`, `steps/<id>/stderr.txt`
- Story resolves from `story_ref.story_path` (relative to the scenario,
  walking ancestors) or `--story`; recording refuses on a
  `story_ref.content_hash` mismatch
- `--format` flips to `json` under `CI` unless passed explicitly
- Exit codes (leaf-local, differ from the parent tree): 0 recorded,
  1 unsupported `schema_version`, 2 scenario parse or validation, 3 usage,
  4 io, 5 story error
- Annotations: side effect `write`, idempotent `no`

## Neighbours

- `hop.top/kit/go/conformance/recorder`: the recording engine
- `hop.top/kit/go/conformance/scenario`, `hop.top/kit/go/conformance/story`: input DSLs
- `hop.top/kit/go/console/cli/conformance/grade`: consumes the output directory
