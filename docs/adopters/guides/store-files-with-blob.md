# Store files and large payloads with blob

> Audience: Go tool authors who need to persist files, uploads or
> backups under a key. Layer 2 of
> [storage abstractions](../concepts/storage-abstractions.md); start
> there if unsure blob is the right layer. Package README:
> [`go/storage/blob`](../../../go/storage/blob/README.md).

`blob.Store` streams bytes in and out under a string key. Pick a backend
by constructor; there is no `Open` registry.

## Pick a backend

| Backend | Constructor | Use when |
|---------|-------------|----------|
| `blob/local` | `local.New(dir)` | one host, a directory, atomic writes |
| `blob/s3` | `s3.New(client, bucket, prefix)` | durable or shared storage on S3 |

Both satisfy the same five-method interface:

```go
type Store interface {
    Put(ctx context.Context, key string, r io.Reader, contentType string) error
    Get(ctx context.Context, key string) (io.ReadCloser, error)
    Delete(ctx context.Context, key string) error
    List(ctx context.Context, prefix string) ([]Object, error)
    Exists(ctx context.Context, key string) (bool, error)
}
```

`Object` carries `Key`, `Size`, `ContentType`.

`List`'s `ContentType` is best-effort: `local` fills it, `s3` leaves it empty
(`ListObjectsV2` omits it). To read a content type reliably, use the optional
`blob.Stater` (next section).

## Read a content type: `Stater`

`Stat` is not on `Store`, so adapters that cannot report metadata are not
forced to implement it. `local` and `s3` do; check with a type assertion:

```go
if s, ok := store.(blob.Stater); ok {
    obj, err := s.Stat(ctx, "reports/q1.txt")
    if err != nil {
        return err // missing key: errors.Is(err, blob.ErrNotFound), like Get
    }
    fmt.Println(obj.ContentType, obj.Size)
}
```

On s3 this is one `HeadObject`, so `List` stays cheap. For a store you do
not control, `blob.Stat(ctx, store, key)` uses `Stater` when present and
otherwise falls back to `List` with an exact-key match (type best-effort,
possibly empty). A custom adapter opts in by adding
`Stat(ctx, key) (blob.Object, error)` and running `blobtest.RunStat`.

## Write, read, list

Code against `blob.Store`; choose the backend once at startup.

```go
import (
    "context"
    "io"
    "strings"

    "hop.top/kit/go/storage/blob"
    "hop.top/kit/go/storage/blob/local"
)

func demo(dir string) error {
    var store blob.Store
    store, err := local.New(dir) // creates dir if missing
    if err != nil {
        return err
    }
    ctx := context.Background()

    if err := store.Put(ctx, "reports/q1.txt", strings.NewReader("ok"), "text/plain"); err != nil {
        return err
    }
    rc, err := store.Get(ctx, "reports/q1.txt")
    if err != nil {
        return err
    }
    defer rc.Close() // caller closes
    b, _ := io.ReadAll(rc)
    _ = b

    objs, _ := store.List(ctx, "reports/") // prefix match on key
    _ = objs
    ok, _ := store.Exists(ctx, "reports/q1.txt")
    _ = ok
    return store.Delete(ctx, "reports/q1.txt")
}
```

Keys may contain `/`; backends create intermediate directories or key
prefixes. `List` is a plain string-prefix match over every key.

## Use S3

Build the AWS client with `config.LoadDefaultConfig`; credentials,
region and endpoint stay in the AWS config chain.

```go
import (
    "github.com/aws/aws-sdk-go-v2/config"
    awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

    kitS3 "hop.top/kit/go/storage/blob/s3"
)

cfg, err := config.LoadDefaultConfig(ctx)
if err != nil { return err }
store := kitS3.New(awss3.NewFromConfig(cfg), "my-bucket", "tool-data")
```

Keys are joined under the prefix with `path.Join` (a trailing `/` on the
prefix is ignored); `List` strips the prefix again, so callers see the
same keys they wrote. `List("logs/")` matches only keys that start with
`logs/`. `List` paginates internally.

### Rust

Feature `blob-s3` in `hop-top-kit` provides the same backend without the
AWS SDK. It signs requests itself and sends them through the netpolicy
guarded client, so `--offline` applies. Credentials, region and endpoint
come from `S3Config` or the standard `AWS_*` variables; there is no
profile, SSO or instance-metadata chain.

```rust
use hop_top_kit::blob::s3::{S3Config, S3Store};
use hop_top_kit::blob::Store;
use hop_top_kit::netpolicy::NetPolicy;

// AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_REGION; AWS_ENDPOINT_URL_S3
// switches to a custom endpoint with path-style addressing (LocalStack, MinIO).
let cfg = S3Config::from_env("my-bucket")?.with_prefix("tool-data");
let store = S3Store::new(cfg, NetPolicy::online())?;
store.put("reports/q1.txt", "hello".as_bytes(), "text/plain")?;
```

Same semantics as Go: prefix join, string-prefix `list` with pagination,
idempotent `delete`, native content type (`list` leaves it empty;
`Stater::stat` reads it). Differences: `put` and `get` hold the whole
object in memory (single `PutObject`, 5 GiB cap), a key that climbs out of
the prefix with `..` is rejected, and the API is synchronous (call it in
`spawn_blocking` from async code). Details:
[Rust blob README](../../../sdk/rs/src/blob/README.md).

## Behaviors that differ by backend

| Behavior | `local` | `s3` |
|----------|---------|------|
| `Put` | atomic: synced temp file renamed into place | single `PutObject` with the content type |
| `Delete` of missing key | succeeds | succeeds |
| `Get` / `Stat` of missing key | error matching `blob.ErrNotFound` (and `fs.ErrNotExist`) | error matching `blob.ErrNotFound` (wraps the SDK `NoSuchKey` / `NotFound`) |
| content type | kept in a `.blob-meta/` sidecar; returned by `List` and `Stat` | sent on `Put`; empty on `List` (`ListObjectsV2` omits it); `Stat` (HEAD) returns it |
| key escaping root | `../` keys rejected before I/O | n/a |

Practical consequences:

- `Delete` is idempotent on every backend: a missing key is not an
  error, so no `Exists` guard is needed.
- For backend-neutral missing-key checks, use
  `errors.Is(err, blob.ErrNotFound)` on the `Get` or `Stat` error. It
  holds on every backend; `local` also keeps `fs.ErrNotExist` for older
  callers. Rust: match `BlobError::NotFound`. Prefer `Exists` when you
  only need presence.
- `local` returns the content type from `List`; S3 does not (empty).
  Treat empty as "unknown" in backend-neutral code, or read it with
  `Stat` (`blob.Stater`) on stores that implement it. `local` reserves the top-level `.blob-meta/`
  directory for sidecars: do not use it as a key prefix (rejected).
  Stores written before sidecars existed report an empty type.
- `local` hides in-flight `.tmp` staging files from `List`, and a
  failed `Put` leaves the previous value intact. Concurrent readers see
  the old or new blob, never a partial one.

## Use with backups

Anything that takes a destination takes a `blob.Store`. The SQLite JSON
store backs up to one; see
[Back up and restore a SQLite JSON store](use-the-sqlite-json-store.md).

## Testing

`local` runs embedded: point `local.New` at `t.TempDir()`. The S3
integration test needs `S3_TEST_BUCKET`, AWS credentials and `-tags s3`.
Both backends run the shared suite `blob/blobtest`; a new backend passes
it with `blobtest.Run(t, func(t *testing.T) blob.Store { ... })`; a
backend implementing `Stater` also passes `blobtest.RunStat`.

In Rust, `tests/blobtest/` ports that suite and runs it on `local` and on
S3. The S3 run replays committed xrr cassettes, so `cargo test` needs no
network; re-record against LocalStack with `XRR_MODE=record` (steps in the
[Rust SDK reference](../reference/rs-sdk.md#blob-s3-backend)).

## See also

- [Storage abstractions](../concepts/storage-abstractions.md): all five
  layers and how to choose.
- [`blob/local`](../../../go/storage/blob/local/README.md),
  [`blob/s3`](../../../go/storage/blob/s3/README.md): backend READMEs.
