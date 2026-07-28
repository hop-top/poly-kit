# blob

object storage integration (S3, local, etc.).

## Writes are atomic

`blob/local`'s `Put` stages contents in a sibling temp file, syncs it, then
renames it over the destination. Two guarantees follow: a concurrent `Get`
never observes a partial blob, and an interrupted or failed write leaves
any previous value intact rather than truncating it.

Temp files are named with a leading dot and a `.tmp` suffix, and `List`
filters them out, so an in-flight write is never visible as a key. Failed
writes remove their temp file.

Ports of this backend are expected to match the same semantics; the Rust
SDK's local backend does.
