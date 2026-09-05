# id

## What it answers

How are identifiers minted and parsed so every port agrees on the TypeID
prefix grammar and suffix encoding? Install identity for telemetry is
`hop_top_kit::telemetry` (install_id), not a TypeID.

## Use it when

- a record needs a new, sortable, prefixed identifier → `id::new("task")`
- an incoming string must be split into prefix and UUID → `id::parse(s)`
- a field may only ever hold one prefix → `Typed<T>` with a `Prefix` marker type
- an existing UUID must be re-encoded under a prefix → `id::from_uuid(prefix, uuid)`
- the id must travel as JSON → `Typed<T>` serialises as the bare string, never an object

## Quick start

```rust
use hop_top_kit::id::{new, parse, IdError};

let id = new("task").unwrap();
assert!(id.starts_with("task_"));

let parsed = parse(&id).unwrap();
assert_eq!(parsed.prefix, "task");
assert_eq!(parsed.uuid.get_version_num(), 7);

assert!(matches!(new("Task"), Err(IdError::InvalidPrefix(_))));
```

## Contract

- Feature `id` pulls in `mti`, `uuid`, `serde`, `thiserror`. Authority: the crate
  [feature table](../../README.md#features).
- Jetify TypeID v0.3.0: UUIDv7 by default, 26-char Crockford base32 suffix,
  prefix grammar `^[a-z]([a-z0-9_]*[a-z0-9])?$` with at most 63 chars.
- Three error kinds, the same in every port: `InvalidPrefix`, `InvalidSuffix`, `PrefixMismatch`.
- URI composition (`<scheme>://<entity-type>/<typeid>`) is not part of this module; compose with
  `hop-top-cite` (feature `uri`) instead.
- Parity: [`contracts/typeid-v1/fixtures.json`](../../../../../contracts/typeid-v1/fixtures.json),
  replayed by `tests/contract.rs`.

## Neighbours

- `hop_top_kit::uri` (src/uri.rs): the URI facade that wraps a typeid into a canonical string
- `hop_top_kit::telemetry` (src/telemetry/): `install_id`, a per-install opaque token, not a TypeID

## See also

- Crate README, [Modules](../../README.md#modules)
- [`go/core/id`](../../../../../go/core/id/), the Go reference
