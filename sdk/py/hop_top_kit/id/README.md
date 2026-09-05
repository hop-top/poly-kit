# id

## What it answers

How to mint and parse kit entity IDs so that Go, TypeScript, Rust and PHP produce the same string for the same `(prefix, uuid)`. Canonical wire form: `<prefix>_<26-char-base32>`, a Crockford-base32 UUIDv7 behind a lowercase prefix. Wrong module for URI composition such as `tlc://task/task_...`: use the `hop-top-cite` package.

## Use it when

- new entity: `new("task")`
- validate or split an incoming id: `parse(s)` returns `Parsed(prefix, uuid)` and raises `IdError`
- Pydantic v2 field with prefix enforcement: `id: TypeId["task"]`

## Quick start

```python
from hop_top_kit.id import new, parse

tid = new("task")
print(tid, parse(tid).uuid)
```

## Contract

- Thin wrapper over `typeid-python`; the string returned by `new` is the JSON wire form and round-trips losslessly through `parse`.
- Errors are typed: `InvalidPrefixError`, `InvalidSuffixError`, `PrefixMismatchError`, all `IdError` and `ValueError` subclasses, so Pydantic surfaces them as `ValidationError`.
- No dependency on the URI registry, by design.
- Parity: [`contracts/typeid-v1/fixtures.json`](../../../../contracts/typeid-v1/fixtures.json), replayed by `tests/test_id_contract.py`.

## Neighbours

- `hop-top-cite`: URI composition on top of the canonical string
- `hop_top_kit.sqlstore`: persistence keyed by these ids
- Go reference: `go/core/id`; TypeScript port: [`sdk/ts/src/id`](../../../ts/src/id/README.md)

## See also

- [`docs/announcements/2026-05-typeid-primitive.md`](../../../../docs/announcements/2026-05-typeid-primitive.md)
- [`docs/adopters/reference/go-primitives.md`](../../../../docs/adopters/reference/go-primitives.md)
