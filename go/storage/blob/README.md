# blob

## What it answers

Where do files and large payloads go? `blob.Store` streams `io.Reader`
in and `io.ReadCloser` out under a key, with `Put`/`Get`/`Delete`/`List`/
`Exists` and a content type. Wrong package for small keyed bytes
(`go/storage/kv`), SQL rows (`go/storage/sqldb`) or credentials
(`go/storage/secret`).

## Use it when

| Backend | Constructor | Pick it when |
|---------|-------------|--------------|
| [`local`](local/README.md) | `local.New(dir)` | one host, a directory, atomic writes required |
| [`s3`](s3/README.md) | `s3.New(client, bucket, prefix)` | durable or shared storage on S3 |

There is no `Open` registry: pick the constructor. Backups and media
uploads take a `blob.Store` as destination.

## Quick start

```go
dir, _ := os.MkdirTemp("", "blob")
defer os.RemoveAll(dir)

var store blob.Store
store, err := local.New(dir)
if err != nil {
	panic(err)
}

ctx := context.Background()
_ = store.Put(ctx, "reports/q1.txt", strings.NewReader("ok"), "text/plain")
rc, _ := store.Get(ctx, "reports/q1.txt")
defer rc.Close()
b, _ := io.ReadAll(rc)
fmt.Println(string(b))
// Output: ok
```

## Contract

- Keys may contain `/`; backends create intermediate directories or key prefixes as needed.
- `local.Put` is atomic: staged in a sibling dot-prefixed `.tmp` file, synced, renamed over the destination. A concurrent `Get` never sees a partial blob; a failed write leaves the previous value and removes its temp file; `List` never surfaces temp files. Ports must match this; the Rust SDK's local backend does.
- `Delete` of a missing key succeeds on every backend (S3 semantics); `Exists` reports false, `Get` errors.
- `blob.ErrNotFound` is the cross-backend missing-key sentinel: `Get` and `Stat` (including the `blob.Stat` helper's fallback) of a missing key return an error with `errors.Is(err, blob.ErrNotFound)` true. The backend's own error stays in the chain (`local`: also `fs.ErrNotExist`; `s3`: the SDK `NoSuchKey` / `NotFound`). Other failures (bad key, permissions, network) never match it. The shared suite in `blobtest` asserts it.
- `List` returns caller-side keys; `Object.ContentType` is the type of the latest `Put` from `local` and empty from `s3` (`ListObjectsV2` omits it), never a stale or wrong one. Treat empty as unknown.
- `List`'s `Object.ContentType` is best-effort: `local` fills it, `s3` does not. To read a type reliably use the optional `blob.Stater` (below).
- Callers close the `ReadCloser` from `Get`.
- Every backend runs the shared suite in `blobtest` (`blobtest.Run`); backends that persist the type also run `blobtest.RunContentType`; backends implementing `Stater` also run `blobtest.RunStat`.
- Tests: `local` runs embedded; `s3` needs `S3_TEST_BUCKET`, AWS credentials and `-tags s3`.

## Reading the content type: `Stater`

`Store` stays narrow. Per-object metadata is an optional capability, the
`io.WriterTo` pattern: `local` and `s3` implement it; a custom adapter (GCS,
Azure, ...) opts in by adding the method, and is never forced to.

```go
type Stater interface {
	Stat(ctx context.Context, key string) (Object, error)
}

if s, ok := store.(blob.Stater); ok {
	obj, err := s.Stat(ctx, "reports/q1.txt") // exact ContentType of the latest Put
	...
}
```

- `Stat` returns the same `Object` as `List` (`Key`, `Size`, `ContentType`); a missing key is an error matching `blob.ErrNotFound`, as with `Get` (`local`: also `errors.Is(err, fs.ErrNotExist)`).
- `s3.Stat` is one `HeadObject`; `List` stays lean (no per-object HEAD). S3 may report its own default (`binary/octet-stream`) for an object stored without a type.
- `blob.Stat(ctx, store, key)` is a helper: uses `Stater` when present, else `List(key)` and an exact-key match, where `ContentType` is only as good as `List`'s (possibly empty) and every key sharing the prefix is listed.
- Adapters that implement `Stater` run `blobtest.RunStat`; s3's runs under `-tags s3`.

## Neighbours

- `hop.top/kit/go/storage/kv`: keyed bytes without streaming.
- `hop.top/kit/go/storage/httpcache`: HTTP response cache, not a blob store.

## See also

- [Storage abstractions](../../../docs/adopters/concepts/storage-abstractions.md)
