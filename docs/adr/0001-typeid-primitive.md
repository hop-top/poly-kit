# ADR 0001: TypeID as kit's entity-ID primitive

- **Status**: Accepted
- **Date**: 2026-05-20
- **Refs**: `tlc/T-0747` (track `id-typeid`)

## Context

Tools built on `kit` (`tlc`, `fin`, `inv`, `ctxt`, and forthcoming
adopters) each mint their own entity identifiers. Today they roll
opaque ULIDs or UUIDs:

- bus events carry IDs like `01J5XK7P3...` with no type signal;
- support workflows need a separate lookup just to know whether
  `f47ac10b-58cc-4372-a567-0e02b2c3d479` is a task, invoice, or
  context blob;
- cross-tool correlation (e.g. an `inv` invoice referencing a `tlc`
  task) requires out-of-band schema agreement.

Stripe's `cus_…`, `inv_…`, `evt_…` convention solves this by encoding
type into the ID. Jetify's **TypeID** specification generalises that
idea: `prefix_<base32(UUIDv7)>`. We want to standardise on it across
all kit-using tools so that:

- logs and bus payloads are self-describing
  (`invoice_01j5xk…`, `task_01j6…`);
- new tools get a turnkey ID primitive without each picking a
  different scheme;
- IDs compose cleanly into the hop-top / poly-uri canonical form
  (`<scheme>://<entity-type>/<typeid>`).

`kit` is the only sensible home for the primitive because it already
owns the cross-language parity contract for Go + 4 SDKs
(`rs`, `ts`, `py`, `php`).

## Decision

Adopt **Jetify TypeID specification v0.3.0** as the canonical wire
format for entity identifiers in every kit-using tool, with one
reference library per kit language target.

### Spec pin

- Specification: <https://github.com/jetify-com/typeid> — version
  **v0.3.0** of `spec/`.
- Suffix: UUIDv7, 128 bits, encoded as 26-character lowercase
  Crockford base32.
- Prefix: either **empty** (suffix-only canonical form) **or**
  matches `^[a-z]([a-z0-9_]*[a-z0-9])?$` with max 63 characters.
  Underscores allowed inside the prefix; the **last** underscore in
  the canonical string is the delimiter between prefix and suffix.
- Canonical string form: `<prefix>_<26-char-base32>`. When the
  prefix is empty, the bare 26-char suffix is the canonical form.
- **Kit API empty-prefix policy**: per shipped SDK behaviour, the
  empty prefix is accepted by `new("")` in Go (`go/core/id`),
  TypeScript (`sdk/ts/src/id`), and Python (`sdk/py/hop_top_kit/id`),
  matching upstream `typeid-js`/`typeid-go`/`typeid-python`
  permissiveness. Rust (`sdk/experimental/rs/src/id`)
  **rejects** the empty prefix in `new()` with
  `IdError::InvalidPrefix` — adopters that need a suffix-only ID in
  Rust must construct it through the upstream `mti` crate directly.
  PHP follows upstream `jewei/typeid-php`. Bus payloads SHOULD
  always carry a prefix regardless of binding.

### Kit API shape (cross-language)

Every binding exposes the same conceptual surface, named idiomatically
for the language:

| Concept                                | Purpose                                            |
| -------------------------------------- | -------------------------------------------------- |
| `new(prefix: string) -> string`        | Generate canonical string (UUIDv7 suffix).         |
| `parse(s: string) -> {prefix, uuid}`   | Round-trip into prefix + backing UUIDv7.           |
| `Typed<P>` / `Typed[P]` / `Typed<T>`   | Per-binding prefix-safe form (template-literal,    |
|                                        | generic, phantom-typed; see per-language sections).|
| `newTyped<P>` / `parseTyped<P>`        | Typed constructors / parsers with prefix check.    |
| Serde / JSON / Pydantic / Zod          | Wire form is the bare canonical string, never an   |
|                                        | object.                                            |

**Non-API**: there is **no `uri()` helper on this primitive**. Tools
that want a poly-uri (`tlc://task/task_01j6…`) call `hop-top-uri`
directly with `(scheme, namespace, typeid_string)`. This keeps the
typeid primitive free of any transitive dependency on the URI
registry, so a tool can depend on `kit`'s id module without
dragging in poly-uri parsing.

### Wire form

- **JSON / bus payloads** carry the canonical string. No `bytes`,
  no struct-with-fields. A field holding a TypeID is a JSON string.
- **SQL / storage** is unconstrained by this ADR (see Non-goals).
  A tool may store the UUIDv7 as `uuid` and reconstruct the
  canonical string at the edge, or store the string directly —
  both are fine.
- **CLI output** uses the canonical string in human and `--json`
  modes.

### URI form

TypeIDs compose into hop-top / poly-uri canonical URIs as:

```
<scheme>://<entity-type>/<typeid-canonical-string>
```

Per the poly-uri spec, **namespace is mandatory** and
`namespace_segments=1`. The simplest convention — and the one we
adopt across kit-using tools — is **entity-type-as-namespace**, so
the namespace segment equals the typeid prefix.

Examples:

| Tool   | Entity   | TypeID                                      | URI                                                                  |
| ------ | -------- | ------------------------------------------- | -------------------------------------------------------------------- |
| `tlc`  | task     | `task_01j6xk7p3m0h2q9n4t5v6w7y8z`           | `tlc://task/task_01j6xk7p3m0h2q9n4t5v6w7y8z`                         |
| `inv`  | invoice  | `invoice_01j5xk7p3m0h2q9n4t5v6w7y8z`        | `inv://invoice/invoice_01j5xk7p3m0h2q9n4t5v6w7y8z`                   |
| `fin`  | txn      | `txn_01j7ab2cd3e4f5g6h7j8k9m0n1`            | `fin://txn/txn_01j7ab2cd3e4f5g6h7j8k9m0n1`                           |
| `ctxt` | snippet  | `snip_01j8pq7r8s9t0u1v2w3x4y5z6a`           | `ctxt://snip/snip_01j8pq7r8s9t0u1v2w3x4y5z6a`                        |

**Scope boundary**: this primitive owns only the `<typeid-canonical-string>`
segment. Scheme registration and `<entity-type>/` namespace parsing
are owned by `hop-top-uri`.

## Per-language reference implementations

These are the five SDK targets the parallel implementation tracks
will ship. Each must expose the kit API shape above, wire the
canonical string for serialisation, and add an integration test
proving cross-language string equality.

### Go — `go.jetify.com/typeid` (v1)

- **Module**: `go.jetify.com/typeid` v1, pinned to **v1.3.0**.
  v2 is still pre-release (`2.0.0-alpha.3` as of this ADR) and
  lacks the stable generic `Typed[P]` surface; v1.3.0 ships the
  full feature set we wrap.
- **Pin**: `go.jetify.com/typeid v1.3.0` in `go.mod`. Patch-level
  upgrades via dependabot; minor bumps via ADR addendum.
- **Kit module**: `go/core/id` (package `id`, imported as
  `hop.top/kit/go/core/id`).
- **API shape** (from `go/core/id/id.go`):
  - `id.New(prefix string) (string, error)` —
    generate; returns the canonical string. Empty prefix is allowed
    (suffix-only canonical form), matching upstream.
  - `id.MustNew(prefix string) string` — panic-on-error variant.
  - `id.Parse(s string) (Parsed, error)` — returns
    `Parsed{Prefix string; UUID uuid.UUID}` where `UUID` is
    `github.com/google/uuid.UUID` (not the upstream gofrs type).
  - `id.Typed[T id.Prefixer]` — generic newtype keyed on a
    zero-sized marker implementing
    `id.Prefixer { Prefix() string }`:
    ```go
    type taskPrefix struct{}
    func (taskPrefix) Prefix() string { return "task" }

    type TaskID = id.Typed[taskPrefix]

    tid, _ := id.NewTyped[taskPrefix]()      // task_01j…
    parsed, _ := id.ParseTyped[taskPrefix]("task_01j…")
    ```
  - Accessors on `Typed[T]`: `String()`, `Prefix()`,
    `UUID() (uuid.UUID, error)`, `IsZero() bool`.
  - `id.NewTyped[T]() (Typed[T], error)` /
    `id.MustNewTyped[T]() Typed[T]` /
    `id.ParseTyped[T](s string) (Typed[T], error)` — typed
    constructors / parser; `ParseTyped` rejects wire-prefix
    mismatch with an error.
- **Serialisation**: `Typed[T]` implements `json.Marshaler` /
  `json.Unmarshaler` directly, emitting the **bare canonical
  string** (never a `{prefix,uuid}` object). `UnmarshalJSON`
  enforces the wire prefix equals `T.Prefix()`.
- **SQL**: not wired by `go/core/id` — adopters that need
  `database/sql` integration store the canonical string (`text`
  column) or the underlying `uuid.UUID` and reconstruct via
  `id.Parse` at the edge.

### Rust — `mti`

- **Crate**: `mti` on crates.io
  (<https://github.com/Govcraft/mti>), pinned to **v1.1.1** in
  `sdk/experimental/rs/Cargo.toml`. Workspace also publishes
  `typeid-prefix` and `typeid-suffix`; kit pulls `mti` only.
- **Pin**: `mti = "1.1.1"`; patch updates allowed.
- **Kit module**: `sdk/experimental/rs/src/id` (crate path
  `hop_top_kit::id`).
- **API shape** (from `sdk/experimental/rs/src/id/mod.rs`):
  - `id::new(prefix: &str) -> Result<String, IdError>` —
    generate. Empty prefix is **rejected** with
    `IdError::InvalidPrefix` (kit intentionally diverges from
    upstream here so Rust adopters always get a self-describing
    ID).
  - `id::parse(s: &str) -> Result<Parsed, IdError>` —
    returns `Parsed { prefix: String, uuid: uuid::Uuid }`.
  - `id::Prefix` trait with `const PREFIX: &'static str` on a
    zero-sized marker per entity:
    ```rust
    pub struct Task;
    impl Prefix for Task { const PREFIX: &'static str = "task"; }
    pub type TaskId = Typed<Task>;
    let id = TaskId::new()?;
    ```
  - `id::Typed<T: Prefix>` — phantom-typed newtype. Methods:
    `new()`, `parse(&str)`, `as_str()`, `into_inner()`. Implements
    `Debug`, `Display`, `Clone`, `PartialEq`, `Eq`, `Hash`,
    `AsRef<str>`, `FromStr`.
  - `id::IdError` — `InvalidPrefix`, `InvalidSuffix`,
    `PrefixMismatch { expected, got }`. Mirrored across the other
    bindings.
- **Serialisation**: `Typed<T>` implements `serde::Serialize` and
  `serde::Deserialize` directly (no extra feature flag needed at
  the kit layer). Wire form is the bare canonical string in JSON,
  YAML, TOML, etc. Deserialisation enforces the wire prefix
  equals `T::PREFIX`.
- **Bus-factor flag**: `mti` currently has **one contributor**
  (`rrrodzilla`, 44 commits as of 2026-03-29) and no other
  actively-maintained Jetify-spec-compliant Rust crate exists.
  This is a known risk we accept for now. Mitigations:
  1. The dependency surface inside `kit` is small and isolated to
     `sdk/experimental/rs/src/id`. If the crate goes unmaintained
     we can vendor it or swap to a replacement without touching
     adopters.
  2. We track upstream activity at every kit release; if there is no
     commit for 12 months and an open critical bug, the
     `sdk/experimental/rs` maintainer is tasked with publishing a
     fork under the kit org (Apache-2.0 / MIT dual licence is
     preserved by the upstream).
- **Spec compliance**: README explicitly cites TypeID v0.3.0 — no
  ambiguity.

### TypeScript — `typeid-js`

- **Package**: `typeid-js` on npm
  (<https://github.com/jetify-com/typeid-js>), official jetify-com
  publication.
- **Pin**: tracked in `sdk/ts/package.json`. Patch updates via
  dependabot; semver-major only via ADR addendum. Requires
  TypeScript ≥ 5.0 for template-literal type narrowing.
- **Kit module**: `sdk/ts/src/id` (subpath import
  `@hop-top/kit/id`).
- **API shape** (from `sdk/ts/src/id/index.ts`): kit's TS surface
  is **functional and string-based**, not a class. It builds on
  top of `typeid-js` but exposes plain strings + a
  template-literal `Typed<P>` for compile-time prefix safety,
  avoiding per-ID class allocations on hot paths.
  - `newId(prefix: string): string` — generate the canonical
    string.
  - `parse(s: string): { prefix: string; uuid: string }` —
    `uuid` is the canonical hyphenated 8-4-4-4-12 form.
  - Template-literal `Typed<P>` type:

    ```ts
    type Typed<P extends string> = `${P}_${string}`;
    ```

    A `Typed<'task'>` is statically distinguishable from
    `Typed<'invoice'>` even though both are `string` at runtime.
  - `newTyped<P extends string>(prefix: P): Typed<P>` — typed
    constructor.
  - `parseTyped<P extends string>(prefix: P, s: string): Typed<P>`
    — typed parser; throws on prefix mismatch (delegates to
    `typeid-js`'s `TypeID.fromString(s, prefix)` for
    single-source-of-truth validation).
  - `typeIdSchema(prefix: string): z.ZodString` — Zod schema
    validating the `${prefix}_<26-char Crockford base32>`
    structural form. Use it on payload boundaries; use `parse` if
    you also need to decode the backing UUIDv7.
- **Serialisation**: kit's TS values are already plain strings, so
  `JSON.stringify(id)` emits the canonical form with no special
  wiring. Zod schemas via `typeIdSchema(prefix)` handle
  validation on the receive side.

### Python — `typeid-python`

- **Package**: `typeid-python` on PyPI
  (<https://github.com/akhundMurad/typeid-python>), community
  reference. Latest release at the time of this ADR is **v0.3.9**.
- **Pin**: `typeid-python>=0.3.9,<0.4` in `sdk/py/pyproject.toml`.
- **Kit module**: `sdk/py/hop_top_kit/id/` (flat layout, no `src/`
  shim). Public surface re-exported from
  `hop_top_kit.id.__init__`.
- **API shape** (from `sdk/py/hop_top_kit/id/_core.py`):
  - `new(prefix: str) -> str` — generate; returns the canonical
    string. Empty prefix is accepted (forwarded as `None` to
    upstream `TypeID(prefix=None)`).
  - `parse(s: str) -> Parsed` — returns a frozen dataclass
    `Parsed(prefix: str, uuid: uuid.UUID)`. The `uuid` field is
    the **stdlib** `uuid.UUID` (decoded from
    `tid.uuid_bytes`) so callers don't need to import
    `uuid_utils`.
  - `Typed[P]` — generic phantom-typed alias for the canonical
    string; runtime value is `str`. Pair with `typing.NewType`
    for stronger type-checker distinction.
  - Errors: `IdError` (base), `InvalidPrefixError`,
    `InvalidSuffixError`, `PrefixMismatchError`. Mirrors the
    cross-language taxonomy; upstream exceptions are translated
    in `_translate()`.
- **Pydantic v2**: an annotated `TypeId` integration is exposed
  alongside `_core` for adopters that want field-level prefix
  validation on Pydantic models.
- **Rust-accelerated**: upstream uses `uuid-utils` (Rust-backed)
  for UUID work — no extra step for adopters.

### PHP — `jewei/typeid-php`

- **Status**: PHP has **no official jetify-com SDK**.
  `jewei/typeid-php` is the community implementation kit adopts
  (PHP 8.4, packagist-published, zero runtime deps, MIT licence).
- **Pin**: `"jewei/typeid-php": "^1.1"` in
  `sdk/experimental/php/composer.json` (current packagist line
  is **v1.1.5**). Patch updates allowed; minor bumps via ADR
  addendum.
- **Kit module**: `sdk/experimental/php` (PSR-4 namespace
  `HopTop\Kit\`).
- **Acceptance criteria** carried by `jewei/typeid-php`:
  - Implements spec **v0.3.0** (prefix grammar, base32 encoding,
    UUIDv7 default).
  - Passes the published `spec/valid.json` and `spec/invalid.json`
    conformance vectors from the jetify-com/typeid repo.
  - Round-trips with the Go, TS, Python, and Rust references over
    the shared fixture (committed to `contracts/typeid/`).
  - MIT licence (kit-compatible).
- **API shape** (per shipped kit-php typeid wrapper, mirroring the
  rest of the bindings): `new(prefix)`, `parse($s)`, a
  `Typed`-equivalent abstract base for per-entity subclasses,
  and a `JsonSerializable` implementation returning the canonical
  string.
- **Typed variant**: PHP has no generics; kit ships a typed
  abstract base that adopters subclass per entity
  (`class TaskId extends TypedTypeId { protected const PREFIX = 'task'; }`).
- **Serialisation**: `JsonSerializable` on the primitive returns
  the canonical string. Kit wires this in
  `sdk/experimental/php` if upstream omits it.

## Migration posture

- **New tools** (e.g. `inv`): TypeID is the **default** primitive
  for entity IDs from day one. Bus events, REST surfaces, and CLI
  output all emit canonical strings.
- **Existing tools** (`tlc`, `ctxt`, `fin`): opt in **per release**,
  not retroactively. Each tool publishes a release note when it
  migrates, and provides a one-time `migrate-ids` subcommand if
  on-disk state contained legacy ULIDs.
- **Bus payload compatibility**: the bus contract allows IDs to be
  any string; switching from ULID to TypeID is forward-compatible
  for consumers that already treat the field as opaque, and adds
  signal for new consumers that want to dispatch on prefix.

## Non-goals

- **Replacing internal `uuid.UUID` at storage layer.** Tools may
  keep storing `uuid` columns; this ADR only standardises the
  **wire form** across tools.
- **Defining a registry of prefixes.** Each tool owns its
  prefixes. Cross-tool conflicts (e.g. two tools both wanting
  `task_…`) are resolved by the scheme + namespace components of
  the poly-uri, not by the typeid prefix alone.
- **URI parsing.** Owned by `hop-top-uri`.
- **Mandating a per-prefix Go / Rust newtype.** Tools may use the
  primitive type directly when type safety isn't worth the
  boilerplate.

## Consequences

### Positive

- Logs, traces, bus events, support tickets, and cross-tool
  references all carry self-describing IDs.
- Adopters get one cross-language ID primitive instead of
  reinventing per project.
- Composes cleanly with poly-uri without bloating the typeid
  primitive's dependency surface.
- UUIDv7 backing gives K-sortable IDs — better database index
  locality than UUIDv4.

### Negative

- Adds an upstream dependency per language. Surface is small but
  non-zero.
- Rust has a real bus-factor risk (mitigated above; tracked).
- PHP has no official jetify-com upstream; we accept a community
  implementation (`jewei/typeid-php`) and carry the maintenance
  watch ourselves.
- Existing tools incur a migration cost (per-release opt-in keeps
  it from being a flag day).

### Neutral

- Storage is unconstrained — adopters choose `uuid` vs string
  per tool.

## Alternatives considered

1. **ULIDs everywhere.** What we have today (de facto). Rejected:
   no type signal, no cross-tool correlation gain.
2. **Stripe-style ad-hoc prefixes (no spec).** E.g. `inv_<random>`
   with no defined encoding. Rejected: no spec → no parity tests,
   no cross-language interop guarantees.
3. **NanoID + prefix.** NanoID has no spec backing for the prefix
   convention and isn't time-sortable. Rejected.
4. **KSUID.** Time-sortable, but no type prefix and no widely
   shared cross-language spec. Rejected on the same grounds as ULID.
5. **Build our own.** Rejected on cost / parity-test burden.
   Jetify's spec is good enough, has multiple implementations, and
   has community traction.

## References

- Jetify TypeID specification:
  <https://github.com/jetify-com/typeid> (`spec/` v0.3.0).
- Go SDK: <https://github.com/jetify-com/typeid-go> ·
  pkg: `go.jetify.com/typeid` (v1.3.0).
- TS SDK: <https://github.com/jetify-com/typeid-js> · npm: `typeid-js`.
- Python SDK: <https://github.com/akhundMurad/typeid-python> ·
  PyPI: `typeid-python`.
- Rust SDK: <https://github.com/Govcraft/mti> · crates.io: `mti`.
- PHP candidate: <https://github.com/jewei/typeid-php> ·
  packagist: `jewei/typeid-php`.
- UUIDv7 draft:
  <https://www.ietf.org/archive/id/draft-peabody-dispatch-new-uuid-format-04.html>.
- Stripe ID prefix convention (prior art):
  <https://stripe.com/docs/api>.
- `tlc/T-0747`, track `id-typeid`.
