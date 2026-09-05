# registry

## What it answers

What does `kv.Open` say when the requested driver was never imported? This
directory holds only a test, kept in its own package so the blank imports
in `kv`'s tests cannot populate the registry here. Not a package to
import; the drivers live in `go/storage/kv/{sqlite,badger,etcd,tidb}`.

## Use it when

- you add a shipped driver: extend the table in `registry_test.go` so the missing-driver message names its import path
- you change `kv.Open`'s error text: this test pins that it names the package, not a build tag

## Contract

- With no driver imported, `kv.Backends()` is empty.
- Opening `sqlite`, `badger`, `etcd` or `tidb` without its driver returns an error naming `hop.top/kit/go/storage/kv/<driver>` and never mentions a build tag.
- Tests: `go test ./go/storage/kv/registry/`; no external service.

## Neighbours

- `hop.top/kit/go/storage/kv`: `Open`, `OpenContext`, `Backends` and the `driverPackages` table under test.

## See also

- [`kv/README.md`](../README.md)
