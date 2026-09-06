# testfake

## What it answers

The recording implementations of the `sideeffect` seams for unit tests: every call lands in a `[]Call`, canned responses queue through `Expect*`, and `Allow` predicates fail the test on anything unexpected. Wrong package for previews (use `dryrun`) and for production (use `real`).

## Use it when

- a test must prove a command wrote a file → `testfake.NewFS(t)` then `testfake.AssertCalled(t, fs.Calls(), match)`
- a test must prove nothing else happened → `.Allow(pred)`; a call is rejected when at least one predicate is registered and none match
- a test must inject a failure → `bus.Expect(err)`, `exec.ExpectRun(err)`, `http.Expect(resp, err)`

## Quick start

```go
func writeConfig(fs sideeffect.FS) error {
	return fs.WriteFile("/etc/app.yaml", []byte("key: v"), 0o600)
}

func TestWriteConfig(t *testing.T) {
	fs := testfake.NewFS(t).Allow(func(c testfake.Call) bool {
		return c.Method == "FS.WriteFile" // anything else fails the test
	})
	if err := writeConfig(fs); err != nil {
		t.Fatal(err)
	}
	testfake.AssertCalled(t, fs.Calls(), func(c testfake.Call) bool {
		return c.Method == "FS.WriteFile" && c.Args[0] == "/etc/app.yaml"
	})
}
```

Verified by `example_test.go` in this directory (runs as a test, since the constructors take `testing.TB`).

## Contract

- `Call.Method` is `<Interface>.<Method>` (`FS.WriteFile`, `HTTP.Do`, `Bus.Publish`, `Exec.Run`); `Args` follow declaration order.
- Defaults after the expectation queue empties: nil error, `200 OK` with an empty JSON body, empty `[]byte` for `Output`; override with `SetDefault*`.
- Every impl is goroutine-safe.

## Neighbours

- `hop.top/kit/go/runtime/sideeffect/real`: production behaviour.
- `hop.top/kit/go/runtime/sideeffect/dryrun`: tolerant preview behaviour, the inverse of `Allow`.
