# grade

## What it answers

How the `kit conformance grade` leaf uploads a recorded cassette to a grading
service and surfaces the verdict. Go test binaries that need the verdict on
the go-test exit code call `hop.top/kit/go/conformance/client` directly.

## Use it when

- grading from a shell or CI → `kit conformance grade <cassette-dir> --service <url>`
- posting the verdict to a PR → add `--pr-comment` and/or `--status-check`
- mounting the leaf on a custom root → `root.AddCommand(grade.Cmd())`

## Quick start

```go
cmd := grade.Cmd()
cmd.SilenceUsage = true
cmd.SilenceErrors = true
cmd.SetArgs([]string{"./cassette", "--tier", "9"})

err := cmd.Execute()
var cliErr *output.Error
if errors.As(err, &cliErr) {
    fmt.Println(cliErr.Code, cliErr.ExitCode)
    fmt.Println(cliErr.Message)
}
// USAGE 2
// conformance grade: --tier must be 1/2/3, got 9
```

## Contract

- Argument: one cassette directory containing `manifest.yaml`; override
  fields with `--scenario-id`, `--story`, `--tier` (1 to 3, default 1)
- Auth: `--token` or `KIT_CONFORMANCE_TOKEN`; service URL from `--service`
  or `KIT_CONFORMANCE_SERVICE`
- `--pr-comment` and `--status-check` consume `GITHUB_TOKEN`; a missing
  token warns but does not fail the grade
- `--format` defaults to `human`; flips to `json` when `CI` is set and the
  flag is not passed explicitly
- `--timeout` (5m), `--retries` (3, minimum 1), `--max-cassette-size`
- Exit codes: 2 usage; 68 GRADE_FAIL and 69 GRADE_UNGRADABLE come from
  `hop.top/kit/go/conformance/client`
- Annotations: side effect `write-shared`, idempotent `yes`;
  `CheckRunName` and `MarkerComment` identify the check run and comment

## Neighbours

- `hop.top/kit/go/conformance/client`: the upload and verdict seam this leaf wraps
- `hop.top/kit/go/console/cli/conformance/harness/record`: produces the cassette
- `hop.top/kit/go/console/cli/conformance/svc`: the service on the other end
- `hop.top/kit/go/console/output`: `--format`, `--output`, `Error` envelope

## See also

- [Go primitives reference](../../../../../docs/adopters/reference/go-primitives.md)
