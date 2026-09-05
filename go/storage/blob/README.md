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
- Callers close the `ReadCloser` from `Get`.
- Tests: `local` runs embedded; `s3` needs `S3_TEST_BUCKET`, AWS credentials and `-tags s3`.

## Neighbours

- `hop.top/kit/go/storage/kv`: keyed bytes without streaming.
- `hop.top/kit/go/storage/httpcache`: HTTP response cache, not a blob store.

## See also

- [Storage abstractions](../../../docs/adopters/concepts/storage-abstractions.md)
