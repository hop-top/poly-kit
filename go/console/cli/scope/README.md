# scope

## What it answers

"Would the active path policy let this tool read, write or exec this
path?" as a CLI: `kit scope show`, `kit scope check`, `kit scope test`.
The rules themselves (patterns, modes, decisions, YAML loading) live in
`hop.top/kit/go/core/scope`; use that package directly when you need
a decision in Go rather than a command.

## Use it when

- mount the subtree under an adopter root → `root.AddCommand(scope.Cmd())`
- print effective ALLOW/DENY rules and mode → `kit scope show [--tool <name>]`
- gate one path in a script or CI step → `kit scope check <path> [--op read|write|exec]`
- diff verdicts for many paths at once → `kit scope test <path>... [--op ...]`
- map the sentinel errors in `main` → `scope.IsDeniedExit(err)`, `scope.IsUsageError(err)`

## Quick start

```go
restore := scopepkg.SetDefault(scopepkg.New().
    SetMode(scopepkg.Strict).
    Allow("/srv/app/**").
    Deny("/srv/app/secrets/**"))
defer restore()

root := scopecmd.Cmd()
root.SetOut(os.Stdout)
root.SilenceUsage = true
root.SilenceErrors = true
root.SetArgs([]string{"check", "/srv/app/secrets/db.env", "--op", "write"})

err := root.Execute()
fmt.Println("denied:", scopecmd.IsDeniedExit(err))
```

## Contract

- Policy source: `Default()` unless `--tool <name>` is given, which loads
  `FromConfig(<name>)` from `hop.top/kit/go/core/scope`.
- `--op` accepts `read|write|exec` (short `r|w|x`); empty means `read`.
  Anything else is a usage error.
- Exit codes: 0 allowed, 1 denied, 2 usage error. `test` exits 1 when
  any path is denied, or when the mode is strict and any decision is
  unknown.
- Output honors `--format table|json|yaml` through
  `hop.top/kit/go/console/output`. `show` in table mode prints a
  `MODE: <mode>` header line before the rules table; json and yaml wrap
  `mode`, optional `tool`, and `rules`.
- Rules are printed sorted by verdict, then op, then pattern.
- Every subcommand is tagged side-effect `read`, idempotent.
- Default deny patterns come from `contracts/parity/scope-defaults.json`;
  the Go copy in `go/core/scope` is test-pinned to it byte-for-byte.

## Neighbours

- `hop.top/kit/go/core/scope`: Policy, Rule, Op, Mode, Decision, Default,
  FromConfig, SetDefault. Add rule semantics there, not here.
- `hop.top/kit/go/console/cli`: Root, side-effect and idempotency
  annotations used by these subcommands.
- `hop.top/kit/go/console/output`: format dispatch and error envelopes
  that carry the exit codes.
- `hop.top/kit/go/console/cli/policy`: command-level delegation policy
  (side-effect classes, max-ops), a different policy from path scope.

## See also

- [contracts/parity/scope-defaults.json](../../../../contracts/parity/scope-defaults.json)
- [contracts/parity/README.md](../../../../contracts/parity/README.md)
- [docs/adopters/reference/go-primitives.md](../../../../docs/adopters/reference/go-primitives.md)
