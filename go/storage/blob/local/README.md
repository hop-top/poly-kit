# local

## What it answers

`blob.Store` over a directory: one file per key, subdirectories per `/`,
atomic writes. Wrong package for storage shared across hosts
(`go/storage/blob/s3`) or for small keyed bytes (`go/storage/kv`).

## Use it when

- `local.New(dir)`, the directory is created if missing. Concurrent readers: `Put` renames a synced temp file into place, so readers see the old or the new blob, never a partial one

## Quick start

```go
dir, _ := os.MkdirTemp("", "blob-local")
defer os.RemoveAll(dir)

store, err := local.New(dir)
if err != nil {
	panic(err)
}

ctx := context.Background()
_ = store.Put(ctx, "reports/q1.txt", strings.NewReader("ok"), "text/plain")
objs, _ := store.List(ctx, "reports/")
fmt.Println(objs[0].Key, objs[0].Size)
// Output: reports/q1.txt 2
```

## Contract

- Keys that escape the root (`../`) are rejected before any I/O.
- Temp files are dot-prefixed with a `.tmp` suffix; `List` filters them; a failed `Put` removes its own temp file and leaves the previous value. The Rust SDK's local blob backend mirrors this.
- Content type is accepted and discarded; `Object.ContentType` is empty on `List`. Tests: embedded, `t.TempDir`, including partial-write and crash-orphan cases.

## Neighbours

- `hop.top/kit/go/storage/secret/file`: per-key files that are secrets. `hop.top/kit/go/storage/blob`: the interface and the backend table.
