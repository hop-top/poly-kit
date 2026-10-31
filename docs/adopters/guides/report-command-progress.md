# Report command progress

> Audience: Go adopters whose command runs in phases (resolve, download, verify) and should tell
> the operator what it is doing without polluting stdout. Package:
> `hop.top/kit/go/console/progress`. Package README:
> [progress](../../../go/console/progress/README.md).

Progress is metadata, not data. Events go to stderr; the command's result stays on stdout under
[`output`](../reference/output.md). You emit events; kit decides whether they render as human
lines or JSON Lines.

## 1. Emit events from a command

`cli.New` wires a reporter into every command's context. Read it back and call `Emit`:

```go
func fetch(ctx context.Context, name string) error {
    r := progress.FromContext(ctx)

    r.Emit(ctx, progress.Event{Phase: "resolve", Item: name})
    r.Emit(ctx, progress.Event{Phase: "download", Item: name, Bytes: 512 * 1024, Total: 2048 * 1024})

    ok := true
    r.Emit(ctx, progress.Event{Phase: "verify", Item: name, OK: &ok,
        Extra: map[string]any{"algo": "sha256"}})
    return nil
}
```

Call it with `cmd.Context()` from `RunE`:

```go
RunE: func(cmd *cobra.Command, args []string) error {
    if err := fetch(cmd.Context(), args[0]); err != nil {
        return err
    }
    fmt.Fprintln(cmd.OutOrStdout(), "fetched", args[0])
    return nil
},
```

Library code below the command takes a `context.Context` and calls `progress.FromContext`.
When nothing is wired (a unit test, a library caller) `FromContext` returns `Discard()`, so
`Emit` is always safe to call and needs no nil check.

`Event` fields:

| Field | Meaning |
|---|---|
| `Phase` | lowercase noun: `resolve`, `download`, `verify` |
| `At` | filled with the current UTC time when zero |
| `Item` | what the phase is working on |
| `Bytes`, `Total` | progress counters, omitted when zero |
| `OK` | `*bool`; nil means "in progress", pointer to true or false is an outcome |
| `Extra` | free-form map, rendered in JSON Lines only |

`OK` is a pointer on purpose. Use a local `ok := true` as above.

## 2. What the operator sees

The same three events, by flag:

```console
$ demo fetch kit
[resolve] kit
[download] kit (512.0 KiB/2048.0 KiB)
[verify] kit ok
```

```console
$ demo --progress-format json fetch kit
{"phase":"resolve","at":"2026-10-31T09:58:34.866352Z","item":"kit"}
{"phase":"download","at":"2026-10-31T09:58:34.866461Z","item":"kit","bytes":524288,"total":2097152}
{"phase":"verify","at":"2026-10-31T09:58:34.866469Z","item":"kit","ok":true,"extra":{"algo":"sha256"}}
```

The human form is `[phase] item (bytes/total) ok|fail`, every part after the phase optional,
bytes in KiB to one decimal. `Extra` is dropped there. JSON Lines is one event per line, `at` in
RFC 3339 UTC, zero-valued optional fields omitted.

Kit picks the reporter once per command, in this order:

1. `--quiet`: `Discard`. Nothing is written.
2. `--progress-format json`: JSON Lines.
3. `--format json` and no explicit `--progress-format`: JSON Lines.
4. Otherwise: human lines.

So `--format json` makes the whole invocation machine-readable (JSON result on stdout, JSON
events on stderr), while `--format json --progress-format human` keeps readable progress beside
a JSON result. Any `--progress-format` value other than `json` renders as human.

`--progress-format` is hidden from `--help`; `--help-all` lists it. Set
`Config.Disable.Progress` to drop the flag and the wiring entirely. `FromContext` then returns
`Discard()` and your emits become no-ops.

## 3. Rules to keep

- Never choose a renderer in `RunE`. Emit events and let the flags decide.
- Writes go to `Root.Streams.Human`, which is stderr by default. Do not print progress to
  stdout; it breaks pipes and `--format json`.
- `Emit` never returns an error and never blocks the work. Encode failures and closed pipes are
  dropped. Do not use an event as the only record of a failure; return the error.
- Reporters are safe for concurrent use, so goroutines can share one.
- Interactive spinners and bars are a different concern: use the `tui` package for those.

## 4. Test emitted events

Swap in a JSON Lines reporter over a buffer and assert on the lines:

```go
var buf bytes.Buffer
ctx := progress.WithReporter(context.Background(), progress.JSONL(&buf))

if err := fetch(ctx, "kit"); err != nil {
    t.Fatal(err)
}
lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
// len(lines) == 3; decode each into progress.Event or map[string]any
```

Set `At` explicitly on events when you compare whole lines, since `Emit` otherwise stamps the
wall clock. `progress.Human(w)` works the same for the human form, and the package's
`example_test.go` shows both with exact output.

## See also

- [`output` reference](../reference/output.md): the stdout data envelope and `--format`.
- [Go primitives](../reference/go-primitives.md): where progress sits among kit's console
  packages.
