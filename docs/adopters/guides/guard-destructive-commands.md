# Guard destructive commands (delegation safety)

> Audience: Go adopters whose CLI can be driven by an agent or a script
> and who want destructive commands behind explicit confirmation, a
> per-run mutation budget, and an optional policy file. Package
> contract: [`go/console/cli/policy` README](../../../go/console/cli/policy/README.md).

kit gates every mutating command from the command's own declared
side-effect tag. You tag commands; kit supplies the flags and the
refusals. This page covers the tag, the three global flags, a policy
file, a typed confirmation token, and the served-caller variant.

## Prerequisites

- A CLI built on `hop.top/kit/go/console/cli` (`cli.New`).
- Every runnable leaf already carries `kit/side-effect`
  (`cli.SetSideEffect`). Untagged commands bypass the policy gate;
  the validator refuses them separately.

## 1. Tag the command

```go
drop := &cobra.Command{
    Use:   "drop",
    Short: "Drop a table",
    Long:  "Drop a table irreversibly.",
    RunE:  runDrop,
}
cli.SetSideEffect(drop, cli.SideEffectDestructive)
cli.SetIdempotency(drop, cli.IdempotencyYes)
```

Tiers: `read`, `write`, `destructive`, `interactive`, plus the
expanded `write-local`, `write-shared`, `destructive-local`,
`destructive-shared`. A tiered value is answered by its own policy
entry, else by its legacy class (`write`, `destructive`).

## 2. Know the default gate

With no flags and no policy, a destructive command asks for
confirmation:

| Situation | Result |
|-----------|--------|
| Terminal available, `--confirm` unset | Prompt `Continue?` |
| No terminal (CI, pipe), `--confirm` unset | Refused: `refused: --confirm=no (or non-TTY default)` |
| `--confirm=yes` or `--confirm=auto` | Runs |
| `--confirm=no` | Refused |
| `--confirm=prompt` | Always prompts |

"Terminal available" means a controlling terminal, not isatty on
stdin: `cmd < file` in a shell still prompts. The prompt reads the
terminal, so piped payload on stdin is never consumed.

The refusal is `UNAUTHORIZED` with transient transience: the caller
clears it by re-running with `--confirm=yes`. A dry run
(`--dry-run`, honored by the leaf) skips the confirmation and is not
counted against the budget.

`--confirm`, `--max-ops`, `--policy` are persistent root flags that
`cli.New` always registers. They are hidden from `--help`, so tell
your users (and agents) they exist.

## 3. Cap mutations per run

```bash
mytool db drop --confirm=yes --max-ops 1
```

`--max-ops N` caps write and destructive commands in one invocation
(0 = unlimited). The count is charged after a command succeeds; once
it exceeds N the run fails with a rate-limited error, exit code 64.
Read commands are never counted.

## 4. Wire a policy file

Install a loader once at construction:

```go
root := cli.New(
    cli.Config{Name: "mytool", Version: "0.1.0", Short: "demo"},
    cli.WithPolicy(cli.DefaultPolicyLoader("mytool")),
)
```

`DefaultPolicyLoader(tool)` resolves `--policy <name>` to
`$XDG_CONFIG_HOME/<tool>/policies/<name>.yaml`
(`policy.Resolve`, `policy.LoadNamed`). Without `WithPolicy`,
`--policy` fails with a usage error; `--confirm` and `--max-ops`
still work. Pass your own `cli.PolicyLoader` func to load from
elsewhere.

A policy file:

```yaml
name: ops              # default: file stem
max_ops: 5             # per-run cap; --max-ops overrides when > 0
allow:
  destructive: ["db:*"]    # verbs this class may run
  write: ["*"]
require_confirm: ["db:*"]  # force the confirmation gate for these verbs
```

Rules, all in source:

- Verb = command path minus the root name, space-separated:
  `mytool db drop` is `db drop`. `db:*` matches `db` and anything
  below it; other patterns use `path.Match`. A pattern written
  `drop:*` does not match `db drop`.
- No `allow` map permits everything. A class listed with `[]` is
  refused outright. A class not listed is permitted. `read` always
  passes.
- A policy refusal is final: `--confirm=yes` does not override it.
  It is `UNAUTHORIZED: policy: destructive not allowed for db drop`.
- A policy that is missing or malformed is a usage error (the gate
  never ran), not an authorization failure.
- `unannotated: write` makes an untagged command count as a write.

The reserved name `kit-default` is never read from a file:
`policy.KitDefault()` allows reads for unestablished callers, writes
for established ones, and destructive only where the command
declares a `kit/permissions` scope. Under the CLI this refuses
destructive commands; name it to opt in.

## 5. Require a typed token

For commands where `--confirm=yes` is too easy to script:

```go
cli.SetDestructiveToken(drop)
```

kit adds `--confirm-token` to that leaf. Without it the run is
refused, and the message names the token, a 12-hex-char value
derived from the command path (stable across runs, so a script can
precompute it). The matching token alone authorizes the command;
`--confirm` is not consulted. Machine formats get the instruction in
`SuggestedFix` instead of a stderr preview.

## 6. Check before you ship

`policy.NewEngine` is public, so policy files can be unit-tested
without running a tool:

```go
e := policy.NewEngine(p, 1)
allowed, confirm, reason := e.Authorize(drop)
err := e.RecordOp(drop) // errors.Is(err, policy.ErrMaxOpsExceeded)
```

`Engine` is per-invocation and not safe for concurrent use. A runnable
version lives in the package's `example_test.go`.

## 7. Served callers

For commands exposed over a transport, `engine.AuthorizeFor(cmd,
caller)` takes a `*policy.Caller` (`Principal`, `Tenant`, `Scopes`,
`Owner`). A `callers:` section in the policy gives per-caller rules
(`principal`, `tenant`, `scope` matchers, plus `allow`, `max_ops`,
`window`, `require_declared_scope`); the first matching rule wins and
a nil caller gets the policy's own rules. `engine.BudgetFor(caller)`
returns that caller's windowed budget and
`engine.RefusedForEveryone(cmd)` lets a transport hide commands no
caller could run. A `permissions:` block carries CEL rules compiled by
`go/console/cli/celpermission`. Schema details are in the package
README; transport setup is in
[secure-remote-serving](secure-remote-serving.md).

## Gotchas

- `--force` is not part of this gate. `cli.SafetyGuard` (which reads a
  `--force` flag) is deprecated and unwired; use the tag and
  `--confirm`.
- `service start` is exempt from the policy gate: its `--policy`
  names the policy the served surfaces enforce, not a check on
  starting them.
- A tool whose tree fails `cli.New` validation stops at startup; fix
  tags before testing the gate.

## See also

- [`go/console/cli/policy` README](../../../go/console/cli/policy/README.md)
- [choose-enforcement-mode](choose-enforcement-mode.md) (bus topics,
  separate engine)
- [cli-api-reference](../reference/cli-api-reference.md)
