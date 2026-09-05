# scope

## What it answers

Which filesystem paths a tool or agent may read, write or execute. How much
and how fast an operation may run is `go/core/breaker`; what text may leave
the process is `go/core/redact`.

## Use it when

- build a policy in code → `scope.New().Allow(...).Deny(...)`
- decide or enforce on a path → `Policy.Check(path, op)`, `Policy.Enforce(path, op)`
- use or swap the process-wide policy → `scope.Default()`, `scope.SetDefault(p)`
- load a declarative policy → `scope.FromConfig("mytool")`
- build patterns → `scope.SecretPaths()`, `scope.ToolConfig("mytool")`, `scope.UserDocs()`, `scope.SystemDirs()`
- inspect from the shell → `kit scope show | check | test`

## Quick start

```go
p := scope.New().
    Allow(scope.UserDocs()...).
    Deny(scope.SecretPaths()...)

if err := p.Enforce("/Users/me/Documents/report.md", scope.OpWrite); err != nil {
    return err
}
```

## Contract

- Deny-wins: a matching deny rule beats any allow rule; no match is Unknown.
- Strict (default) denies Unknown and returns `ErrDenied`; Warn logs and allows; Prompt calls the registered `PromptFunc`, and a missing callback denies.
- Symlinks resolve at `Check` time; a nonexistent path resolves through its deepest existing ancestor so deny rules match by intent.
- Patterns are doublestar v4; `~` expands to home, and `%APPDATA%`, `%LOCALAPPDATA%`, `%USERPROFILE%` expand on Windows.
- `init()` pre-populates `scope.Default()` with `SecretPaths()` denied, so linking the package hardens the binary.
- [`scope-defaults.json`](scope-defaults.json) is the canonical polyglot deny list; the TS and Python ports load [`contracts/parity/scope-defaults.json`](../../../contracts/parity/scope-defaults.json).
- `scope.yaml`: `/etc/xdg/<tool>/scope.yaml` read first, user config merged over it; per-user `mode` wins, rules append.
- `kit scope` exit codes: `0` allowed, `1` denied, `2` usage error.

## Neighbours

- `go/core/breaker`: volume, rate and circuit guardrails; neither package imports the other, FS-touching code consults both.
- `go/core/redact`: secrets and PII leaving the process.
- `go/core/xdg`: where the `tool:*` config macros resolve; `xdg.SetGuard` wires the xdg to scope hook.
- `go/console/cli/scope`: the `kit scope` subcommand tree.

## See also

- [Scope reference](../../../docs/adopters/reference/scope.md): API table, modes, decision algorithm, default deny list, `scope.yaml` schema, CLI, test isolation, escape hatches
- [Go primitives index](../../../docs/adopters/reference/go-primitives.md#i-need-guardrails-on-what-my-tool-can-do)
