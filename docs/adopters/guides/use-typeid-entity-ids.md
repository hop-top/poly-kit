# Use TypeIDs for entity IDs

> Audience: Go adopters minting identifiers for their own entities
> (tasks, invoices, jobs) who want self-describing, sortable IDs that
> read the same in every kit language. Package contract:
> [`go/core/id` README](../../../go/core/id/README.md). Wire contract:
> [typeid-v1](../../../contracts/typeid-v1/README.md).

A TypeID is `prefix_<26-char Crockford base32>`; the suffix is a
UUIDv7, so IDs sort by creation time. The prefix names the entity
(`task_01jg0000...`), so a log line or bus event says what it refers
to without a lookup. Use it instead of bare UUIDs or ULIDs for kit
entity IDs.

## Prerequisites

- Go module that imports `hop.top/kit/go/core/id`.
- A prefix per entity: lowercase letters, digits and underscores,
  starting with a letter, ending with a letter or digit, at most 63
  characters. The calling tool owns its prefixes; there is no central
  registry.

## 1. Mint and parse with runtime prefixes

```go
s, err := id.New("task")        // "task_01j..." canonical string
parsed, err := id.Parse(s)      // parsed.Prefix == "task"
u := parsed.UUID                // google/uuid UUID, version 7
```

`id.MustNew(prefix)` panics instead of returning an error; use it at
init time where a bad prefix is a programmer error. An empty prefix is
valid and yields the bare 26-character suffix. `id.Parse` rejects bad
prefixes, wrong-length suffixes and anything else not in canonical
form.

## 2. Get compile-time prefix safety

Declare a zero-sized prefix carrier and alias `id.Typed` over it:

```go
type taskPrefix struct{}

func (taskPrefix) Prefix() string { return "task" }

type TaskID = id.Typed[taskPrefix]

a := id.MustNewTyped[taskPrefix]()        // or id.NewTyped, with error
b, err := id.ParseTyped[taskPrefix](s)    // error if the prefix is not "task"
```

Now a function taking `TaskID` cannot be handed an `InvoiceID`, and
parsing an `invoice_...` string as a task fails at runtime with
`id: prefix mismatch: want "task", got "invoice"` rather than being
coerced.

Methods on `Typed[T]`: `String()` (canonical string), `Prefix()`,
`UUID()`, `IsZero()`. The type is comparable, so it works as a map
key; canonical strings sort chronologically.

## 3. Serialize

`Typed[T]` marshals to and from the bare canonical string, never a
`{prefix, uuid}` object:

```go
type Task struct {
    ID    TaskID `json:"id"`
    Title string `json:"title"`
}
// {"id":"task_01jg000000e008000000000000","title":"ship it"}
```

Unmarshalling checks the prefix: `{"id":"invoice_..."}` into a `TaskID`
is an error. This is the form on bus events and REST payloads, so
other languages can read it as is.

## 4. Share IDs with other languages

The same `(prefix, uuid)` gives the same canonical string in Go,
Rust, TypeScript, Python and PHP. The shared vectors live in
[`contracts/typeid-v1/fixtures.json`](../../../contracts/typeid-v1/fixtures.json)
(Jetify TypeID spec v0.3); each port runs them as a contract test, and
`make test-parity-typeid` runs all of them. Per-port entry points are
in the
[contract README](../../../contracts/typeid-v1/README.md) and the
[announcement](../../announcements/2026-05-typeid-primitive.md). If
you extend or port the encoding, add vectors there first.

## Gotchas

- The zero `Typed[T]` is not empty to `String()` or JSON: it
  renders as `task_` plus 26 zeros. Test with `IsZero()` before
  marshalling an optional ID, or use a pointer field with
  `omitempty`.
- `id` does not compose URIs such as `tlc://task/task_01j...`. Use
  `hop.top/cite` for that.

## See also

- [`go/core/id` README](../../../go/core/id/README.md)
- [typeid-v1 contract](../../../contracts/typeid-v1/README.md)
- [go-primitives](../reference/go-primitives.md)
