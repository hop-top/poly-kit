# contracts

shared schemas and cross-language constants.

## Contents

Corpora bind every port, Go included; detail lives in each corpus README.

- [httpcache-v1/](httpcache-v1/README.md): kit/storage/httpcache wire
  contract: cache keying, on-store envelope, storage gate.
- [bus-v1/](bus-v1/README.md): kit/runtime/bus topic matching, validator
  error texts, `PrefixTopics`, qualifiers, enforcement modes, event
  envelope JSON. Go and Rust runners.
- [completion-v1/](completion-v1/README.md): kit/console/cli shell
  completion: completers, `__complete` output, `completion <shell>`
  scripts byte for byte. Go and Rust runners.
- [consent-v1/](consent-v1/README.md): kit/core/consent grants: records, decisions, refusals, audit, telemetry migration. Go and Rust runners.
- [config-v1/](config-v1/README.md): kit/core/config scalar parsing and
  typing, overrides, precedence, layered load, set/unset. Go and Rust.
- [netpolicy-v1/](netpolicy-v1/README.md): kit/core/netpolicy `--offline`:
  loopback, dial decision, URL guard, refusal texts. Go and Rust.
- [completion-spec-v1/](completion-spec-v1/README.md): kit/console/cli Fig
  and Usage specs of one fixture tree, byte for byte. Go and Rust runners.
- [statemachine-v1/](statemachine-v1/README.md): kit/runtime/domain state
  machine transitions, error texts, veto wrap, payload JSON. Go and Rust.
- [retry-v1/](retry-v1/README.md): kit/core/util retry backoff corpus:
  delay table, full-jitter bounds, stop rule. Go and Rust runners.
- [kv-v1/](kv-v1/README.md): kit/storage/kv keys bind to SQLite as TEXT,
  written by one SDK and read back by another.
- [identity-v1/](identity-v1/README.md): kit/core/identity HKDF-SHA256 key derivation over an Ed25519 seed.
- [typeid-v1/](typeid-v1/README.md): kit `typeid`: the string every SDK
  makes from `(prefix, uuid)` and recovers it from.
- [proto/](proto/README.md): shared protobuf definitions;
  [commands.proto](proto/cmdsurface/v1/commands.proto) is the cmdsurface
  RPC schema source (Go stubs in `go/transport/cmdsurface/gen/`).
- [ts/](ts/README.md): TypeScript copy of the `crud.v1` protobuf and
  Connect stubs; the SDK builds against `sdk/ts/src/gen/`.
- [parity/](parity/README.md): TUI constants shared across Go/TS/Py.
- [bridge.proto](bridge.proto): kit/bridge wire payload schema; the JSON
  schema for non-Go shells is `bridge.schema.json`.
