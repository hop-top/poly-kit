# id

## What it answers

How to mint and parse kit entity IDs so that Go, Python, Rust and PHP produce the same string for the same `(prefix, uuid)`. Canonical wire form: `<prefix>_<26-char-base32>`, a Crockford-base32 UUIDv7 behind a lowercase prefix. Wrong module for URI composition such as `tlc://task/task_...`: use `@hop-top/cite`.

## Use it when

- new entity: `newId('task')`
- validate or split an incoming id: `parse(s)` returns `{ prefix, uuid }` and throws on malformed input
- compile-time prefix safety: `Typed<'task'>` with `newTyped` / `parseTyped`

## Quick start

```ts
import { newId, parse } from '@hop-top/kit/id';

const id = newId('task');
console.log(id, parse(id).uuid);
```

## Contract

- Thin wrapper over `typeid-js`; the string returned by `newId` is the JSON wire form and round-trips losslessly through `parse`.
- Prefix grammar and length limit follow the typeid spec pinned in the fixture; a divergence is a wire-format break.
- No transitive dependency on the URI registry, by design.
- Parity: [`contracts/typeid-v1/fixtures.json`](../../../../contracts/typeid-v1/fixtures.json), replayed by [`contract.test.ts`](contract.test.ts).

## Neighbours

- `@hop-top/cite`: URI composition on top of the canonical string
- `@hop-top/kit/sqlstore`: persistence keyed by these ids
- Go reference: `go/core/id`; Python port: [`hop_top_kit/id`](../../../py/hop_top_kit/id/README.md)

## See also

- [`docs/announcements/2026-05-typeid-primitive.md`](../../../../docs/announcements/2026-05-typeid-primitive.md)
- [`docs/adopters/reference/go-primitives.md`](../../../../docs/adopters/reference/go-primitives.md)
