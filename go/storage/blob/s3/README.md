# s3

## What it answers

`blob.Store` over one S3 bucket and key prefix, using `aws-sdk-go-v2`.
Wrong package for a single host (`go/storage/blob/local`).

## Use it when

- `s3.New(client, bucket, prefix)` with an `*s3.Client` you built from `config.LoadDefaultConfig`; credentials, region and endpoint stay in the AWS config chain, not in this package
- `List(prefix)` returns key, size and content type from `ListObjectsV2`

## Contract

- Keys are joined under `prefix` with `path.Join`; a missing object on `Get` or `Exists` is reported, not panicked.
- `Put` sends the content type given; `Delete` of a missing key succeeds.
- Tests: `s3_integration_test.go` is behind `//go:build s3` and needs `S3_TEST_BUCKET` plus AWS credentials in the environment; it writes under `blob-test/`. `s3_test.go` (interface assertion) always runs. No cassette.

## Neighbours

- `hop.top/kit/go/storage/blob/local`: same interface, filesystem.
- `hop.top/kit/go/core/netpolicy`: not wired here; an offline run is not refused by this package.

## See also

- [`blob/README.md`](../README.md)
