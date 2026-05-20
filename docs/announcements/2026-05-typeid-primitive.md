# TypeID primitive — kit-go + 4 SDKs

**Date:** 2026-05-20
**Status:** Available
**Track:** [`id-typeid`](../adr/0001-typeid-primitive.md)

## What

The TypeID primitive (Jetify spec v0.3) is now available across the
kit-go module and all four kit SDKs (Rust, TypeScript, Python, PHP).
Use it instead of bare ULIDs / UUIDs for entity identifiers in
kit-using tools.

A TypeID looks like `task_01j5xkv2et008000000000000`: a lowercase
prefix that identifies the entity type, an underscore, and a 26-char
Crockford-base32 encoding of a UUIDv7. The prefix makes IDs
self-describing in logs, bus events, support workflows, and
cross-tool correlation.

## Per-language modules

| Lang | Module | Upstream lib |
|------|--------|---------------|
| Go   | [`go/core/id`](../../go/core/id/)                         | `go.jetify.com/typeid` v1.3.0 |
| Rust | [`sdk/experimental/rs/src/id`](../../sdk/experimental/rs/src/id/) | `mti` v1.1.1 |
| TS   | [`sdk/ts/src/id`](../../sdk/ts/src/id/)                   | `typeid-js` v1.2.0 |
| Py   | [`sdk/py/hop_top_kit/id`](../../sdk/py/hop_top_kit/id/)   | `typeid-python` >=0.3.9 |
| PHP  | [`sdk/experimental/php/src/Id`](../../sdk/experimental/php/src/Id/) | `jewei/typeid-php` v1.1.5 |

Cross-language wire-format parity is locked by
[`contracts/typeid-v1/fixtures.json`](../../contracts/typeid-v1/fixtures.json)
— a wire-incompatible change in any SDK fails the parity gate in CI.

## API shape

Each language exposes the same minimal surface (idiomatic to the
host language). Go example:

```go
import "hop.top/kit/go/core/id"

s, err := id.New("task")            // → "task_01j5xkv2et008000000000000"
parsed, err := id.Parse(s)          // → Parsed{Prefix: "task", UUID: ...}
```

For compile-time prefix safety, each SDK exposes a `Typed` newtype /
generic / template-literal variant. See the per-language module for
specifics.

## URI form

TypeIDs compose into canonical URIs via
[`hop-top/poly-uri`](https://github.com/hop-top/poly-uri):

```
tlc://task/task_01j5xkv2et008000000000000
inv://invoice/invoice_01j5xkv2et008000000000001
```

The TypeID primitive owns only the typeid string portion. URI
parsing and the scheme/namespace registry are handled by `poly-uri`.

## Migration recommendation

**Existing kit-using tools** (`tlc`, `ctxt`, `fin`): opt in per
release. Add a `typeid` column alongside the existing ID; backfill;
flip the primary key after one release cycle of dual-write.

**Tracking issues:**
- [`hop-top/tlc#135`](https://github.com/hop-top/tlc/issues/135) —
  first reference migration

**Greenfield tools** (`inv`): adopt TypeID natively from day one.

## References

- ADR: [`docs/adr/0001-typeid-primitive.md`](../adr/0001-typeid-primitive.md)
- Glossary: TypeID entry in the workspace glossary
- Upstream spec: <https://github.com/jetify-com/typeid>
- Wire-format contract: [`contracts/typeid-v1/fixtures.json`](../../contracts/typeid-v1/fixtures.json)
