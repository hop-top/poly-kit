# `hop.top/kit/go/runtime/sideeffect`

Shared interfaces for the four side-effect categories that account
for ~all kit CLI mutations: `FS`, `HTTP`, `Bus`, `Exec`. Three
implementations of each interface live in sub-packages so the
**same seam** serves both **dry-run preview** and **test mocking**.

> One abstraction, three implementations: `real`, `dryrun`,
> `testfake`. See ADR-0019 for the design rationale.

## Why this exists

A `--dry-run` flag installed without an interception layer is
silently ignored by every command. Adopters write directly to
`os.WriteFile`, dial `*http.Client`, exec subprocesses, and
publish events through whatever publisher they hold; the kit cli
has no way to swap them for "preview-only" equivalents.

This package is the swap point. Adopters consume the interfaces
via dependency injection. The kit cli (or the adopter's own
wiring) picks `real` in production, `dryrun` when `--dry-run` is
set, and `testfake` in unit tests.

## Interfaces

```go
type FS interface {
    WriteFile(path string, data []byte, perm os.FileMode) error
    MkdirAll(path string, perm os.FileMode) error
    Rename(oldpath, newpath string) error
    Remove(path string) error
}

type HTTP interface {
    Do(req *http.Request) (*http.Response, error)
}

type Bus interface {
    Publish(ctx context.Context, topic, source string, payload any) error
}

type Exec interface {
    Run(cmd *exec.Cmd) error
    Output(cmd *exec.Cmd) ([]byte, error)
}
```

Reads (`os.ReadFile`, `os.Stat`, `http.Get`, `http.Head`) pass
through to stdlib unchanged. Dry-run does not pretend reads are
unsafe.

## Implementations

| Sub-package | When to use | Behaviour |
|-------------|-------------|-----------|
| `real` | Production | Delegates to stdlib + kit primitives. Zero overhead. |
| `dryrun` | `--dry-run` mode | Prints a human-readable line per call to a configurable `io.Writer` (default `os.Stderr`). Returns plausibly-shaped synthetic responses. Never blocks; never fails on a "would-be" call. |
| `testfake` | Unit tests | Records every call into a slice. `Calls()`, `AssertCalled`, `AssertNotCalled` helpers. Optional `Allow(...)` predicates fail loudly on unexpected calls via `t.Fatalf`. |

### `real`

```go
import (
    "hop.top/kit/go/runtime/sideeffect"
    "hop.top/kit/go/runtime/sideeffect/real"
)

var fs sideeffect.FS = real.FS{}
var h  sideeffect.HTTP = real.NewHTTP(http.DefaultClient)
var b  sideeffect.Bus  = real.NewBus(myEventPublisher)
var e  sideeffect.Exec = real.Exec{}
```

### `dryrun`

```go
import (
    "os"
    "hop.top/kit/go/runtime/sideeffect/dryrun"
)

fs := dryrun.NewFS(dryrun.WithWriter(os.Stderr))
h  := dryrun.NewHTTP(http.DefaultClient) // GET/HEAD pass through
b  := dryrun.NewBus()                     // augments bus.Qualifiers.Mechanism
e  := dryrun.NewExec()                    // prints argv, returns zero
```

The `Bus` impl augments `Mechanism: "dry_run"` on payloads that
embed `bus.Qualifiers` (per ADR-0017). Payloads without the embed
are described without augmentation; the gap is logged once per
Bus.

### `testfake`

```go
import (
    "testing"
    "hop.top/kit/go/runtime/sideeffect/testfake"
)

func TestMyCommand(t *testing.T) {
    fs := testfake.NewFS(t).Allow(func(c testfake.Call) bool {
        return c.Method == "FS.WriteFile"
    })
    if err := myCommand(fs); err != nil { ... }

    testfake.AssertCalled(t, fs.Calls(), func(c testfake.Call) bool {
        return c.Method == "FS.WriteFile"
    })
}
```

`Allow` predicates are aggregated: a call is rejected when at least
one predicate is registered AND none match. Default (no Allow)
accepts every call.

## The `--dry-run` global flag

`cli.New` registers `--dry-run` as a kit-managed persistent flag
on the root command, bound to viper key `kit.dry_run`. The cli
wrapper installs a `PersistentPreRunE` hook that resolves an
ADR-0020 policy table per dispatched leaf, then tags the command's
context via `sideeffect.WithDryRun(ctx, true)` when the policy
allows. Inside RunE (or any library code), check the flag:

```go
import "hop.top/kit/go/runtime/sideeffect"

if sideeffect.IsDryRun(cmd.Context()) {
    // pick the dryrun impl ...
}
```

`sideeffect.IsDryRun` lives in the sideeffect package — **not**
in `cli` — so library code can branch on dry-run without taking a
cli dependency.

## Policy table (ADR-0020)

| `kit/side-effect` | `--dry-run` behaviour |
|-------------------|------------------------|
| `read`            | silent no-op (flag accepted, ctx untagged) |
| `write`           | supported by default                       |
| `destructive`     | supported by default                       |
| `interactive`     | rejected with friendly diagnostic          |

Plus two annotation overrides:

| Annotation                | Set via                     | Effect                                  |
|---------------------------|-----------------------------|------------------------------------------|
| `kit/dry-run: opted-out`  | `cli.OptOutDryRun(cmd)`     | Reject, point at the explicit decision  |
| `kit/dry-run: supported`  | `cli.SupportsDryRun(cmd)`   | Allow (legacy ADR-0019; one-time warn)  |

## Adoption

The convention from `cli-conventions-with-kit.md` §3.5–§3.6 is
self-sufficient: declare `kit/side-effect` and adopters get
`--dry-run` for free on `write|destructive` leaves. No second
opt-in call is required.

```go
import (
    "github.com/spf13/cobra"
    "hop.top/kit/go/console/cli"
    "hop.top/kit/go/runtime/sideeffect"
    "hop.top/kit/go/runtime/sideeffect/dryrun"
    "hop.top/kit/go/runtime/sideeffect/real"
)

func MyCmd(root *cli.Root) *cobra.Command {
    cmd := &cobra.Command{
        Use: "do",
        RunE: func(cmd *cobra.Command, _ []string) error {
            fs := pickFS(cmd)
            return runDoWithFS(cmd.Context(), fs)
        },
    }
    cli.SetSideEffect(cmd, cli.SideEffectWrite) // opts the leaf in
    return cmd
}

func pickFS(cmd *cobra.Command) sideeffect.FS {
    if sideeffect.IsDryRun(cmd.Context()) {
        return dryrun.NewFS(dryrun.WithWriter(cmd.ErrOrStderr()))
    }
    return real.FS{}
}
```

`cli help <cmd>` shows a "Dry-run support: this command honors
`--dry-run`" line when the resolved policy is `allow` (i.e.
`write|destructive` tier without an opt-out, or the legacy
`SupportsDryRun` annotation).

### Opting out

A `write|destructive` command that genuinely cannot honor
`--dry-run` (compound state half-applied, downstream API without
preview semantics, etc.) calls `cli.OptOutDryRun(cmd)`:

```go
cli.SetSideEffect(cmd, cli.SideEffectDestructive)
cli.OptOutDryRun(cmd) // and document why in a comment
```

The pre-execution hook rejects `--dry-run` with a diagnostic that
points at the explicit decision rather than implying the command
is unmigrated. Adopter doc-comments should explain why the opt-out
is necessary so the audit trail survives.

### Migration from ADR-0019

Adopters that already shipped commands with the ADR-0019
default-deny opt-in have two cleanup paths:

1. **Drop the explicit opt-in.** If the command is already tagged
   `kit/side-effect: write|destructive`, delete the
   `cli.SupportsDryRun(cmd)` line. Behavior is unchanged; a
   one-time deprecation warning fires at startup until you
   migrate.
2. **Keep the legacy annotation.** `cli.SupportsDryRun(cmd)` and
   the `kit/dry-run: supported` annotation continue to work as
   back-compat synonyms for the duration of the deprecation
   window (one minor cycle). The pre-execution hook treats both
   exactly like the tier-driven `allow` path.

The pilots in `cmd/kit/...` (`kit symlink`, `kit init`) take path
(1) — they were the only commands shipping with explicit opt-in
under ADR-0019 and now opt in via the side-effect tier alone.

### Bus auto-tagging

Adopters that publish events get the `Mechanism: "dry_run"`
qualifier automatically by wrapping their `domain.EventPublisher`:

```go
pub = sideeffect.NewDryRunPublisher(pub)
```

The wrapper is a passthrough when the publish-time ctx is **not**
dry-run — no reflection cost, no payload mutation. When ctx is
dry-run AND payload embeds `bus.Qualifiers`, the wrapper sets
`Mechanism = "dry_run"` (preserving any existing non-empty
Mechanism).

Pointer payloads (`*MyEvent`) get in-place augmentation. Value
payloads (`MyEvent`) are best-effort and pass through unchanged —
the wrapper does not clone-and-augment because subscriber pipelines
may rely on the caller's struct identity. Pass `*T` to guarantee
the tag.

### Test pattern

```go
func TestMyCommand_DryRun_DoesNotWrite(t *testing.T) {
    fs := testfake.NewFS(t)
    if err := runDoWithFS(ctx, fs); err != nil { t.Fatal(err) }
    testfake.AssertNotCalled(t, fs.Calls(), func(c testfake.Call) bool {
        return c.Method == "FS.WriteFile"
    })
}
```

## What `--dry-run` guarantees

For commands that have opted in:

- **No real FS writes** routed through `sideeffect.FS`.
- **No real mutating HTTP calls** routed through `sideeffect.HTTP`.
  Safe verbs (GET, HEAD) still hit the network because the preview
  needs the actual response shape.
- **No real subprocess invocations** routed through
  `sideeffect.Exec`.
- **Bus events tagged** with `Mechanism: "dry_run"` (when payload
  embeds `bus.Qualifiers` and is passed by pointer).

## What `--dry-run` does not guarantee

- **Subprocess containment.** A child process spawned outside
  `sideeffect.Exec` (or by a tool the command shells out to) makes
  its own decisions. We print the argv and skip invocation; we do
  not chase children.
- **Compound state correctness.** A `dryrun.HTTP.POST` returning
  a synthetic `201 Created` with empty body means
  create-then-read flows work in dry-run only as deeply as the
  synthesised state goes. Print a banner in your command when in
  dry-run mode so users mentally discount downstream output.
- **Coverage of un-migrated code paths.** A command's RunE that
  bypasses `sideeffect.FS` and calls `os.WriteFile` directly is
  invisible to the swap. Migrate every mutating call site.

## Tier-driven default: every `write|destructive` leaf inherits `--dry-run`

Updated under ADR-0020 (was default-deny under ADR-0019). The
`kit/side-effect` tier kit already validates is the single source
of truth. Adopters who follow the §3.5–§3.6 spec get `--dry-run`
support automatically; the rare command that cannot honor it
declares `cli.OptOutDryRun(cmd)` with a doc-comment explaining
why.

The pilots in `cmd/kit/...` (`kit init`, `kit symlink`) shipped
under ADR-0019; they now opt in via the side-effect tier alone.
Adopter CLIs (`tlc`, `ctxt`, `wsm`, `hop`) inherit `--dry-run`
support automatically once they sync to the kit version that
includes ADR-0020 — no migration step required if their leaves
already carry the side-effect tag.

## References

- ADR-0020 — Unify `--dry-run` opt-in with `kit/side-effect`
  (current policy; supersedes ADR-0019's per-command opt-in
  registry).
- ADR-0019 — `kit/runtime/sideeffect` package and global
  `--dry-run` (parent ADR; partially superseded).
- ADR-0017 — Bus topic naming grammar and Qualifiers payload
  convention (`Mechanism: "dry_run"` is the first consumer of the
  reserved Qualifiers slot).
- `cli-conventions-with-kit.md` §3.5 (`kit/side-effect` tier) and
  §3.6 (auto-wired `--dry-run` on write/destructive leaves).
- `.tlc/tracks/kit-sideeffect-dry-run/plan.md` — original sideeffect
  track plan.
- `.tlc/tracks/kit-dryrun-sideeffect-unify/plan.md` — unification
  track plan.
