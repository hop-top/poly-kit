# output

## What it answers

How a kit CLI renders one result set in every `--format` (`table`, `json`,
`yaml`, `csv`, `text`) with one column order, and how it reports failures
as a structured error envelope with a pinned exit code. Wrong package when
you need the root flags, theme or handler middleware
(`hop.top/kit/go/console/cli`) or a port for another runtime (see
Neighbours).

## Use it when

- a command prints rows → `output.RegisterFlags(cmd, v)` then `output.Dispatch(cmd, v, data)`
- you need a custom or replaced formatter → `output.Default.Override(f)` or `output.NewRegistry()` + `output.WithRegistry(r)`
- a command streams and must not take `-o` → `output.RegisterFlagsWith(cmd, v, output.DisableOutputFlag())`
- you render a themed table on a TTY → `output.Render(w, output.Table, rows, output.WithTableStyle(root.TableStyle()))`
- you return a failure with an exit code → `output.NotFoundError(msg)` or `output.WrapError(err, code, exit)`

## Quick start

```go
output.RegisterFlags(cmd, v)
// ...
return output.Dispatch(cmd, v, data)
```

## Contract

- Column order and header names come from `table:""` tags in struct
  declaration order; `--cols` reorders as well as selects; `header == key`;
  zero rows emits nothing; `priority` hides columns on overflow and never
  reorders them. Full rules and the per-runtime capability table:
  [Column ordering](../../../docs/adopters/reference/output.md#column-ordering).
- Error constructors pin code, exit and transience (`GENERIC` 1, `USAGE` 2,
  `NOT_FOUND` 3, `CONFLICT` 4, `UNAUTHORIZED` 5, `TRANSIENT` 6,
  `RATE_LIMITED` 64, `PROVENANCE_MISSING` 65); the retained error never
  reaches the wire. Table and `errors.Is` semantics:
  [Errors](../../../docs/adopters/reference/output.md#errors).
- The cross-runtime fixtures in `sdk/tests/cross-lang/` compare column
  order re-parsed from each runtime's output, never raw bytes.

## Neighbours

- `hop.top/kit/go/console/cli`: registers these flags on the root, wraps
  handler errors with `WrapError`, exposes `Root.TableStyle()`
- [`sdk/ts/src/output`](../../../sdk/ts/src/output/README.md),
  [`sdk/py/hop_top_kit/output`](../../../sdk/py/hop_top_kit/output/README.md),
  [`sdk/experimental/rs/src/output`](../../../sdk/experimental/rs/src/output/README.md),
  [`sdk/experimental/php/src/Output`](../../../sdk/experimental/php/src/Output/README.md):
  the ports carrying a `ColumnSpec` list

## See also

- [Output API reference](../../../docs/adopters/reference/output.md):
  public API table, quickstart variants, column ordering, errors
- [Go CLI API reference](../../../docs/adopters/reference/cli-api-reference.md)
