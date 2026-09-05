# id

## What it answers

What does a kit entity identifier look like, and how do I get compile-time
prefix safety for mine. URI composition (`tlc://task/task_01j...`) is not
here; compose poly-URIs with `hop.top/cite`.

## Use it when

- you mint an ID with a runtime prefix: `New(prefix)` / `MustNew(prefix)`
- you read one back: `Parse(s)` gives the prefix and the backing `uuid.UUID`
- you want a per-entity type (`TaskID`, `InvoiceID`): a zero-sized `Prefixer` plus `Typed[T]`, then `NewTyped[T]()` / `ParseTyped[T](s)`
- you serialise: `Typed[T]` marshals to and from the bare canonical string

## Quick start

```go
parsed, err := id.Parse("task_01jg000000e008000000000000")
if err != nil {
    fmt.Println("err:", err)
    return
}
fmt.Println(parsed.Prefix)
fmt.Println(parsed.UUID)
// Output:
// task
// 01940000-0000-7000-8000-000000000000
```

`example_test.go` also shows the `Typed[T]` JSON round-trip.

## Contract

- Canonical form: `prefix_<26-char Crockford base32>`, suffix a UUIDv7, so successive IDs are K-sortable. Empty prefix yields the bare suffix.
- Prefix: `^[a-z]([a-z0-9_]*[a-z0-9])?$`, at most 63 characters. Prefixes are owned by the calling tool, not registered centrally.
- The canonical string is the only wire form. `Typed[T]` never marshals a `{prefix, uuid}` object; unmarshalling a mismatched prefix is an error.
- Parity: the same UUIDv7 gives the same canonical string in Go, Rust, TypeScript, Python and PHP. Fixtures: [contracts/typeid-v1/fixtures.json](../../../contracts/typeid-v1/fixtures.json), pinned in `contract_test.go`.
- Backed by `go.jetify.com/typeid` v1.3.0.

## Neighbours

- `hop.top/cite`: URI composition around an ID.
- `sdk/ts`, `sdk/py`, `sdk/experimental/rs`, `sdk/experimental/php`: the `id` ports.

## See also

- [2026-05-typeid-primitive.md](../../../docs/announcements/2026-05-typeid-primitive.md)
- [go-primitives.md](../../../docs/adopters/reference/go-primitives.md)
