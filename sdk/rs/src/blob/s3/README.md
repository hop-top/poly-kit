# blob/s3

## What it answers

How does a kit-rs tool keep blobs on S3 or an S3-compatible server (LocalStack, MinIO) without
linking the AWS SDK? `S3Store` (feature `blob-s3`) implements `blob::Store` and `blob::Stater` over
the S3 REST API, signed here and sent through the netpolicy guarded client, so `--offline` and the
host allowlist apply.

## Files

| File | What it holds |
|------|---------------|
| `mod.rs` | `S3Config` (explicit keys or `AWS_*` env, endpoint, path-style), `S3Store`, response mapping onto `BlobError` |
| `sigv4.rs` | AWS Signature Version 4 for S3, unit-tested against AWS's published vectors |
| `transport.rs` | `Transport`, the one HTTP seam, and `GuardedTransport` over the guarded client |
| `xml.rs` | `ListObjectsV2` and error-body parsing, no XML crate |
| `tests.rs` | unit tests: signing, XML, key mapping, config, pagination, offline refusal |

## See also

- [blob README](../README.md): the `Store` contract every backend keeps, and the S3 summary
- [Rust SDK reference, blob S3 backend](../../../../../docs/adopters/reference/rs-sdk.md#blob-s3-backend): semantics, differences from Go, test cassettes
- [`go/storage/blob/s3`](../../../../../go/storage/blob/s3/README.md), the Go reference
