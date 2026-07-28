# contracts

shared schemas and cross-language constants.

## Contents

- [httpcache-v1/](httpcache-v1/): kit/storage/httpcache wire contract —
  `keying.json` (cache-key derivation), `entry.json` (on-store envelope),
  `cacheability.json` (storage gate). Contract of record for every port,
  including the Go implementation.
- [proto/](proto/README.md): shared protobuf definitions.
- [parity/](parity/README.md): TUI constants shared across Go/TS/Py.
- [bridge.proto](bridge.proto): kit/bridge wire payload — protobuf
  schema (binary wire + semantics). Authoritative JSON schema for
  non-Go shells (Swift Share Extension, Shortcuts) lives alongside in
  `bridge.schema.json`.
