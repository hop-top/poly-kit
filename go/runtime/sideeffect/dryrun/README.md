# dryrun

## What it answers

The preview implementations of the `sideeffect` seams: each call prints one human-readable line and returns a synthetic success. Pick it when `sideeffect.IsDryRun(ctx)` is true. It is the wrong package for tests that must assert on calls (use `testfake`) and for "refuse instead of pretend" semantics (wrap `real` yourself).

## Use it when

- a `write` or `destructive` leaf runs under `--dry-run` → `dryrun.NewFS(dryrun.WithWriter(cmd.ErrOrStderr()))`
- a preview still needs real read responses → `dryrun.NewHTTP(client)`: GET and HEAD pass through, mutating verbs get a synthetic 201
- an event publish must be visible but not delivered → `dryrun.NewBus()`
- a subprocess must be shown, not run → `dryrun.NewExec()`

## Quick start

```go
fs := dryrun.NewFS(dryrun.WithWriter(os.Stdout))
if err := fs.WriteFile("/etc/app.yaml", []byte("key: v"), 0o600); err != nil {
	panic(err) // never happens: dryrun returns nil for would-be calls
}
if err := fs.Remove("/etc/app.yaml"); err != nil {
	panic(err)
}
// [dry-run] would write /etc/app.yaml (6 bytes, mode 0600)
// [dry-run] would remove /etc/app.yaml
```

Verified by `example_test.go` in this directory.

## Contract

- Never blocks, never returns an error for a would-be call; a preview completes even where the real call would fail.
- Output goes to `os.Stderr` unless `WithWriter` says otherwise; `WithWriter(nil)` resets to stderr.
- `Bus.Publish` sets `Mechanism: "dry_run"` on payloads that embed `bus.Qualifiers`; other payloads are described without augmentation and the gap is logged once per Bus.
- `Exec.Output` returns an empty byte slice; subprocess side effects cannot be contained (ADR-0019).

## Neighbours

- `hop.top/kit/go/runtime/sideeffect`: the interfaces and `IsDryRun`.
- `hop.top/kit/go/console/cli`: installs the `--dry-run` flag and the ADR-0020 policy table.
- `hop.top/kit/go/runtime/sideeffect/testfake`: recording impls for tests.
