# ADR 0005: Output column precedence and header/key identity

- **Status**: Accepted
- **Date**: 2026-08-17

## Context

kit renders tabular output from five runtimes: Go (the reference) plus
the TypeScript, Python, Rust and PHP SDKs. Go reads column order off
`table:""` struct tags in field declaration order — the struct *is* the
column spec. The other four take payload-shaped rows (maps, dicts,
associative arrays, `serde_json::Value`) which carry no declaration
order, so each ships a `ColumnSpec` list to supply what Go gets from
field order.

Two questions had drifted apart across the five implementations, and
both were answered inconsistently enough that output differed by
runtime for identical inputs.

**Precedence.** When a caller supplies a `ColumnSpec` list *and* the
user passes `--cols`, which order wins? An audit found this was not the
even split it first appeared to be. Every payload SDK already rendered
in the user's `--cols` order: all five TS formatters iterate the user's
array, as does Python's projection, and Rust and PHP behave the same.
The one schema-order code path in TS was unreachable dead code. Go alone
built a lookup set from the requested names and then iterated columns in
struct order — its own doc comment stated the behavior outright: "Order
is preserved from cols (struct field order), not from selected." So the
split was four-to-one with the reference runtime as the sole outlier,
not a genuine design disagreement.

Supporting evidence pointed the same way: both Go and Python already
computed an *ordered*, deduplicated `--cols` list, and Go pinned that
ordering with a test — then discarded the order at projection time. The
machinery to honor user order existed and was inert.

**Header vs key.** Each payload SDK's `ColumnSpec` carried two names:
`header` (the printed label) and `key` (the property to read off the
row). This invites a split — `header="Name"`, `key="name"` — and TS's
projection implemented one, mapping `out[c.header] = r[c.key]`. Go
cannot express such a split at all: a `table:""` tag supplies the header
while the value comes from the struct field the tag sits on, and there
is no second name to give. Meanwhile every `ColumnSpec` construction
site in the repo already set `header == key`; the sole exception existed
only to assert a default `priority` value and never rendered.

The split also had a hidden cost beyond aesthetics. With two names, the
name validated against `--cols` and the name used for row lookup can
diverge, so a column can pass validation at the dispatch layer and then
fail mid-render. It also forces `ColumnSpec` objects themselves through
to every formatter, since a plain list of resolved names can no longer
express the mapping.

## Decision

**1. `--cols` reorders as well as selects; the user's order wins.**
`--cols status,name` renders `status` then `name`, whatever the
`ColumnSpec` list or struct declaration says. The same rule applies on
the no-schema fallback path, with no exception. Absent `--cols`, order
comes from the `ColumnSpec` list (Go: struct declaration order), and
payload key order is the fallback only when no spec was supplied at all.

Precedence resolves **once, at the dispatch layer**, into a single
ordered list of column names passed through the existing `cols`
parameter. Formatter signatures are unchanged in every SDK, so
third-party formatters keep working and inherit correct ordering with
no code change.

**2. `header == key`, universally.** The two are one name: the printed
label, the value matched against `--cols`, and the key read off the row.
Every SDK rejects a mismatch at construction rather than tolerating
drift — Python raises `ValueError`, PHP throws
`InvalidArgumentException`, Rust panics in `ColumnSpec::new`, and TS
retains `key` for source compatibility while requiring it to equal
`header`.

The governing principle is that **no SDK may carry a capability the
reference runtime cannot mirror.** Go's inability to express a
header/key split is not a gap to work around in the payload SDKs; it is
the reason the constraint binds all five. Keeping the ports perfectly
aligned is worth more than a naming convenience that only four of five
runtimes could ever offer.

## Consequences

- **Output bytes change.** Runtimes that previously emitted columns in
  payload key order — or, in Rust's case, alphabetically, because
  `serde_json::Map` is a `BTreeMap` without the `preserve_order`
  feature — now follow the caller's `ColumnSpec` order. Anyone parsing
  column positions or diffing `--format json` output sees different
  bytes. This is user-visible and documented in each SDK's changelog.
- **Rust enables `serde_json/preserve_order`**, which is additive within
  a Cargo build graph: it changes JSON key order for every consumer of
  that graph, not only output-layer callers. Wiring `ColumnSpec` alone
  would not have made Rust correct, since a fresh `Map` re-sorts on
  insert at every nesting depth.
- **Validation and lookup collapse into one operation.** A name accepted
  by `--cols` validation can no longer fail again mid-render, and
  resolving precedence to a list of plain names — rather than threading
  `ColumnSpec` objects to every formatter — is only sound because the
  two names are one.
- **Dead code was deleted rather than made reachable.** Under
  `header == key`, TS's header-to-key map is an identity map and its
  projection an identity mapping; its schema-order filter contradicted
  the live formatters and was removed. Unknown-column rejection was
  never lost — it lives in `--cols` validation.
- **Enforcement lives in cross-runtime fixtures**, not in the shared
  constants file. A constants entry could only assert that a file
  contains the string it contains; it could never assert that a runtime
  orders columns correctly. The fixtures render each case in each
  runtime, re-parse the runtime's own emitted bytes with order-preserving
  readers, and compare the observed sequences element-wise — never
  sorting, never using set membership or deep equality, both of which
  are blind to order. Bytes are deliberately not compared across
  runtimes: table padding and YAML block style differ legitimately, so
  column *order* is what is pinned and nothing more. Byte-level
  formatting parity remains the job of each SDK's own unit tests — `csv`
  quoting, for one, agrees across Go, Python and TS in the default LF
  mode but diverges under the `crlf` option.

### Acknowledged quirks

- **`priority` remains Go-only.** It drives hide-on-overflow column
  dropping in the reference runtime and never reorders anything. The
  payload SDKs accept and store the field and ignore it, so specs stay
  portable until the behavior is ported. This is in tension with the
  principle above — it is a capability one runtime has and four do not —
  and the tension is deliberate and temporary. Note the asymmetry runs
  the *safe* direction: the reference has the extra capability, so no
  SDK expresses something Go cannot mirror.
- **Rule 1 is unobservable in Go.** A struct has exactly one field
  order, so "spec order differs from payload order" cannot be
  constructed there. Go satisfies the rule by construction.
- **Rule 2 required two independent fixes in Go**, and finding only one
  would have left the contract half-honored. The column filter iterated
  in struct order rather than the requested order, which governs
  `table`, `csv` and `text`; separately, struct-to-map projection
  returned an unordered map, which alphabetizes `json` and `yaml`
  regardless of the first. Go now emits user order in all five formats,
  projecting `json` and `yaml` through an ordered carrier rather than a
  plain map. Rule 2 holds universally, with no format-scoped narrowing.
- **The ordered-column affordance on the `--template` path is not yet
  uniform.** Go exposes `.Cols` and Python and TS expose `cols` —
  iterable column *names*. PHP's minimal renderer offers `{*}`, which
  expands to pre-joined row *values*; that is a weaker, different
  affordance, PHP-specific for now. Rust's minimal renderer exposes
  nothing at all: it receives the row but not the schema, and since
  `--template` and `--cols` are mutually exclusive, the spec is the only
  ordering signal on that path. Neither minimal substituter can expose
  an iterable column list without a real template engine, so a five-way
  spelling is not reachable at the current tier and remains open.
- **Format coverage is not uniform.** Go, Python and TS ship `table`,
  `json`, `yaml`, `csv` and `text`; Rust and PHP ship the first three
  only. This ADR governs column ordering wherever a formatter exists; it
  does not assert that every format exists everywhere.
