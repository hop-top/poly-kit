# contracts

shared schemas and cross-language constants.

## Contents

Corpora are contracts of record for every port, Go included; detail lives
in each corpus's own README.

- [httpcache-v1/](httpcache-v1/README.md): kit/storage/httpcache wire
  contract: cache keying, on-store envelope, storage gate.
- [bus-v1/](bus-v1/README.md): kit/runtime/bus conformance corpus: topic
  matching and validators with error texts, `PrefixTopics`, qualifiers,
  enforcement-mode parsing, event envelope JSON. Go and Rust runners.
- [completion-v1/](completion-v1/README.md): kit/console/cli shell
  completion corpus: completer outputs, `__complete` wire output over
  command trees, `completion <shell>` scripts byte for byte. Go and Rust
  runners.
- [config-v1/](config-v1/README.md): kit/core/config corpus: scalar
  parsing and typing, overrides, precedence, layered load, set/unset.
  Go and Rust runners.
- [netpolicy-v1/](netpolicy-v1/README.md): kit/core/netpolicy `--offline`
  corpus: loopback classification, dial decision, offline URL guard,
  refusal texts. Go and Rust runners.
- [completion-spec-v1/](completion-spec-v1/README.md): kit/console/cli
  completion-spec exports: one fixture tree, its Fig module and Usage KDL
  byte for byte. Go runner.
- [retry-v1/](retry-v1/README.md): kit/core/util retry backoff corpus:
  delay table, full-jitter bounds, stop rule. Go and Rust runners.
- [kv-v1/](kv-v1/README.md): kit/storage/kv storage-binding corpus: keys
  bind to SQLite as TEXT, written by one SDK and read back by another.
- [identity-v1/](identity-v1/README.md): kit/core/identity key-derivation
  vectors (HKDF-SHA256 over an Ed25519 seed).
- [typeid-v1/](typeid-v1/README.md): kit `typeid` vectors: the string
  every SDK produces for `(prefix, uuid)` and recovers from it.
- [proto/](proto/README.md): shared protobuf definitions;
  [commands.proto](proto/cmdsurface/v1/commands.proto) is the cmdsurface
  RPC schema source (Go stubs in `go/transport/cmdsurface/gen/`).
- [ts/](ts/README.md): TypeScript copy of the `crud.v1` protobuf and
  Connect stubs; the SDK builds against `sdk/ts/src/gen/`.
- [parity/](parity/README.md): TUI constants shared across Go/TS/Py.
- [bridge.proto](bridge.proto): kit/bridge wire payload schema; the JSON
  schema for non-Go shells is `bridge.schema.json`.
