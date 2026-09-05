# progress

## What it answers

How a long-running command reports per-phase progress on stderr without
touching the stdout data envelope, and without knowing whether the operator
asked for human lines or JSONL. Data output belongs to
`hop.top/kit/go/console/output`; spinners and bars for interactive screens
belong to `hop.top/kit/go/console/tui`.

## Use it when

- a leaf command has phases worth reporting → `progress.FromContext(cmd.Context()).Emit(ctx, progress.Event{Phase: "resolve", Item: target})`
- a library function deep below the command needs to report → accept a
  `context.Context`; `FromContext` returns `Discard()` when nothing is wired
- a test wants to assert emitted events → `progress.WithReporter(ctx, progress.JSONL(&buf))`
- output must be silenced → `progress.Discard()`

## Quick start

```go
var buf bytes.Buffer
ctx := progress.WithReporter(context.Background(), progress.JSONL(&buf))

r := progress.FromContext(ctx)
r.Emit(ctx, progress.Event{
    Phase: "download",
    At:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
    Item:  "kit.tar.gz",
    Bytes: 512 * 1024,
    Total: 2048 * 1024,
})
fmt.Print(buf.String())
```

Prints one line:
`{"phase":"download","at":"2026-01-02T03:04:05Z","item":"kit.tar.gz","bytes":524288,"total":2097152}`.

## Contract

- Reporter selection is done by `hop.top/kit/go/console/cli` per command:
  `--quiet` wins (Discard); `--progress-format json` forces JSONL;
  `--format json` implies JSONL unless `--progress-format human` is explicit;
  otherwise Human. Adopters never pick a renderer in RunE.
- Writers are stderr. Stdout is reserved for the data envelope.
- JSONL: one `Event` per line, trailing `\n`, `at` as RFC 3339 in UTC,
  zero-valued optional fields omitted. `At` is filled by `Emit` when zero.
- Human: `[phase] item (bytes/total) ok|fail`; parts after the phase are
  optional; bytes render as `<n>.<d> KiB`.
- `Emit` never returns an error and never blocks the work: encoding failures
  and closed-pipe writes are dropped.
- Reporters are safe for concurrent use.
- Phase names are lowercase nouns (`resolve`, `download`, `verify`).

## Neighbours

- `hop.top/kit/go/console/cli`: registers `--progress-format`, wires the
  Reporter into `cmd.Context()`, `Disable.Progress` suppresses the flag
- `hop.top/kit/go/console/output`: stdout data envelope and `--format`
- `hop.top/kit/go/console/tui`: interactive progress widgets

## See also

- [Go primitives reference](../../../docs/adopters/reference/go-primitives.md)
