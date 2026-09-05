# svc

## What it answers

How the grading service in `hop.top/kit/go/conformance/svc` is bound to
operator flags and the kit binary as `kit conformance svc {serve,token}`.
The service logic itself lives in that package; this one only parses flags
and wires stores.

## Use it when

- starting the HTTP grader → `kit conformance svc serve --scenarios-root <dir> --claims-db <sqlite>`
- issuing a bearer token → `kit conformance svc token mint --tenant <t> --scope grade:<team>`
- auditing or revoking claims → `token list`, `token revoke <token_id>`
- mounting on a custom root → `root.AddCommand(svc.Cmd())`

## Quick start

```go
cmd := svc.Cmd()
cmd.SilenceUsage = true
cmd.SilenceErrors = true
cmd.SetArgs([]string{"serve", "--claims-db", "/tmp/claims.sqlite"})

err := cmd.Execute()
var cliErr *output.Error
if errors.As(err, &cliErr) {
    fmt.Println(cliErr.Code, cliErr.ExitCode)
    fmt.Println(cliErr.Message)
}
// USAGE 2
// --scenarios-root is required (or KIT_CONF_SVC_SCENARIOS_ROOT)
```

## Contract

- `serve` flags: `--port` (0 = auto), `--addr`, `--scenarios-root`
  (`KIT_CONF_SVC_SCENARIOS_ROOT`, required), `--claims-db`
  (`KIT_CONF_SVC_CLAIMS_DB`, required), `--judges-config`
  (`KIT_CONF_SVC_JUDGES_CONFIG`; omitted means every AI judge is refused),
  `--hard-cap-mb` (64), `--soft-cap-mb` (8)
- `serve` prints one JSON line on stdout at startup: `port`, `pid`,
  `version`, `scenarios_loaded`; shuts down on context cancel with a 30s grace
- `token mint` prints the plaintext token once; `token list` shows
  `token_id`, sha256 prefix and scopes, never plaintext
- Exit codes: 2 missing required flag, 1 store, judges config or listen failure
- All leaves are exempt from Layer-A validation; `serve` is annotated
  side effect `write`, idempotent `no`

## Neighbours

- `hop.top/kit/go/conformance/svc`: service, `FSStore`, `SQLClaimStore`, judges
- `hop.top/kit/go/transport/api`: router and middleware `serve` mounts on
- `hop.top/kit/go/console/cli/conformance/grade`: the client side
