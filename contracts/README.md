# contracts

shared schemas and cross-language constants.

## Contents

- [httpcache-v1/](httpcache-v1/): kit/storage/httpcache wire contract —
  `keying.json` (cache-key derivation), `entry.json` (on-store envelope),
  `cacheability.json` (storage gate). Contract of record for every port,
  including the Go implementation.
- [kv-v1/](kv-v1/): kit/storage/kv cross-language storage-binding corpus —
  `keys.json` (key/value cases in hex, expected `ORDER BY key` sequence,
  prefix-scan expectations). Contract of record for how keys bind to
  SQLite: they must go in as TEXT, because SQLite compares storage class
  before value and a BLOB-bound key silently misses a TEXT-bound one. Each
  SDK writes the corpus and another reads it back, since a suite that
  round-trips within one language cannot catch a binding mismatch.
- [identity-v1/](identity-v1/): kit/core/identity key-derivation vectors —
  `derive-key.json` (HKDF-SHA256 over an Ed25519 seed, `info` string and
  output length, with public keys so a port can confirm keypair
  construction before checking derivation). Contract of record for every
  port; encryption itself uses a random nonce and so is covered by
  round-trip tests rather than fixed vectors.
- [proto/](proto/README.md): shared protobuf definitions.
- [parity/](parity/README.md): TUI constants shared across Go/TS/Py.
- [bridge.proto](bridge.proto): kit/bridge wire payload — protobuf
  schema (binary wire + semantics). Authoritative JSON schema for
  non-Go shells (Swift Share Extension, Shortcuts) lives alongside in
  `bridge.schema.json`.
