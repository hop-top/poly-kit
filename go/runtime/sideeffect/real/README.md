# real

## What it answers

The production implementations of the `sideeffect` seams: every method delegates straight to `os`, `os/exec`, `net/http`, or a `domain.EventPublisher`, with no instrumentation. Pick it at the boundary when `--dry-run` is off and you are not in a test; otherwise pick `dryrun` or `testfake`.

## Use it when

- wiring a command in production → `real.FS{}`, `real.Exec{}`, `real.NewHTTP(client)`, `real.NewBus(pub)`
- the bus publisher may be nil during bootstrap → `real.NewBus(nil)`; `Publish` returns `ErrNilPublisher` instead of panicking

## Quick start

```go
dir, _ := os.MkdirTemp("", "real")
defer os.RemoveAll(dir)

var fs sideeffect.FS = real.FS{} // zero value delegates to os
path := filepath.Join(dir, "out.txt")
if err := fs.WriteFile(path, []byte("hello"), 0o600); err != nil {
	panic(err)
}
data, _ := os.ReadFile(path)
fmt.Println(string(data))
// hello
```

Verified by `example_test.go` in this directory.

## Contract

- Zero values of `FS`, `Exec`, and `HTTP` are usable; `HTTP{}` falls back to `http.DefaultClient`.
- No retries, no logging, no redaction: compose those around the interface, not inside this package.

## Neighbours

- `hop.top/kit/go/runtime/sideeffect/dryrun`: prints what would happen, returns synthetic success.
- `hop.top/kit/go/runtime/sideeffect/testfake`: records calls, fails loudly on unexpected ones.
