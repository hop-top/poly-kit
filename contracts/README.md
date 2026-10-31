# contracts

shared schemas and cross-language constants.

## Contents

Corpora bind every port, Go included; detail lives in each corpus README.

- [httpcache-v1/](httpcache-v1/README.md): kit/storage/httpcache wire contract: cache keying, on-store envelope, storage gate.
- [bus-v1/](bus-v1/README.md): kit/runtime/bus topic matching, validator error texts, `PrefixTopics`, qualifiers, enforcement modes, event envelope JSON. Go and Rust runners.
- [blob-v1/](blob-v1/README.md): kit/storage/blob local store: put/get/delete/list/exists, key escapes, list order. Go and Rust runners.
- [cli-root-v1/](cli-root-v1/README.md): kit/console/cli root at the command line: persistent flags, version, help groups, aliases, flag-error envelopes, autocorrect. Go, Rust; TS, Py, PHP replay delegation vectors.
- [celpermission-v1/](celpermission-v1/README.md): kit/console/cli `permissions:` rule gate for served invocations: bindings, deny-overrides, fail-closed evaluation, load-time refusals. Go and Rust runners.
- [completion-v1/](completion-v1/README.md): kit/console/cli shell completion: completers, `__complete` output, `completion <shell>` scripts byte for byte. Go and Rust runners.
- [consent-v1/](consent-v1/README.md): kit/core/consent grants: records, decisions, refusals, audit,
  telemetry migration. Go and Rust runners.
- [config-v1/](config-v1/README.md): kit/core/config scalars, overrides, precedence, layered load. Go and Rust.
- [netpolicy-v1/](netpolicy-v1/README.md): kit/core/netpolicy `--offline`: loopback, dial decision, URL guard,
  refusal texts. Go and Rust.
- [completion-spec-v1/](completion-spec-v1/README.md): kit/console/cli Fig and Usage specs, byte for byte.
  Go and Rust runners.
- [statemachine-v1/](statemachine-v1/README.md): kit/runtime/domain state machine transitions, error texts,
  veto wrap, payload JSON. Go and Rust.
- [breaker-v1/](breaker-v1/README.md): kit/core/breaker on a fake clock.
- [retry-v1/](retry-v1/README.md): kit/core/util retry backoff corpus: delay table, full-jitter bounds, stop
  rule. Go and Rust runners.
- [authn-v1/](authn-v1/README.md): kit/transport/authn signed tokens per algorithm, PEM key ids, API key hashes. Go, Rust.
- [webhook-v1/](webhook-v1/README.md): kit/transport/webhook receiver: Graph, Pipedrive v2 and generic HMAC request sequences, HMAC digests. Go, Rust.
- [redact-v1/](redact-v1/README.md): kit/core/redact policy dump, spans, output. Go, Rust.
- [upgrade-v1/](upgrade-v1/README.md): kit/core/upgrade versioned state migrations: version order, pending
  range, run, rollback, prune and error texts. Go and Rust runners.
- [kv-v1/](kv-v1/README.md): kit/storage/kv keys bind to SQLite as TEXT; one SDK writes, another reads.
- [identity-v1/](identity-v1/README.md): kit/core/identity HKDF-SHA256 key derivation over an Ed25519 seed.
- [typeid-v1/](typeid-v1/README.md): kit `typeid`: the string every SDK makes from `(prefix, uuid)` and back.
- [alias-v1/](alias-v1/README.md): kit alias bridge options, name validation, cycle detection; four runners.
- [proto/](proto/README.md): shared protobuf definitions, incl. the cmdsurface RPC schema
  ([commands.proto](proto/cmdsurface/v1/commands.proto)).
- [ts/](ts/README.md): TypeScript copy of the `crud.v1` protobuf and Connect stubs; the SDK builds against `sdk/ts/src/gen/`.
- [parity/](parity/README.md): TUI constants shared across Go/TS/Py.
- [bridge.proto](bridge.proto): kit/bridge wire payload (JSON: `bridge.schema.json`).
