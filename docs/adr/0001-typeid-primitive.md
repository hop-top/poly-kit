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
  (`invoice_01J5XK…`, `task_01J6…`);
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
- Suffix: UUIDv7, 128 bits, encoded as 26-character Crockford
  base32.
- Prefix: matches `^[a-z]([a-z0-9_]*[a-z0-9])?$`, max 63 characters.
  Underscores allowed inside; the **last** underscore is the
  delimiter between prefix and suffix.
- Canonical string form: `<prefix>_<26-char-base32>`. When the
  prefix is empty, the bare 26-char suffix is the canonical form.

### Kit API shape (cross-language)

Every binding exposes the same conceptual surface, named idiomatically
for the language:

| Concept                           | Purpose                                            |
| --------------------------------- | -------------------------------------------------- |
| `new(prefix: string) -> TypeID`   | Generate (UUIDv7 suffix).                          |
| `parse(s: string) -> TypeID`      | Round-trip from canonical string.                  |
| `prefix() / suffix() / string()`  | Accessors. `string()` returns canonical form.      |
| Typed / branded newtype           | Compile-time prefix safety.                        |
| Serde / JSON serialiser           | Default to string form, never binary.              |

**Non-API**: there is **no `uri()` helper on this primitive**. Tools
that want a poly-uri (`tlc://task/task_01J6…`) call `hop-top-uri`
directly with `(scheme, namespace, typeid_string)`. This keeps the
typeid primitive free of any transitive dependency on the URI
registry, so a tool can depend on `kit/typeid` without dragging in
poly-uri parsing.

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
| `tlc`  | task     | `task_01J6XK7P3M0H2Q9N4T5V6W7Y8Z`           | `tlc://task/task_01J6XK7P3M0H2Q9N4T5V6W7Y8Z`                         |
| `inv`  | invoice  | `invoice_01J5XK7P3M0H2Q9N4T5V6W7Y8Z`        | `inv://invoice/invoice_01J5XK7P3M0H2Q9N4T5V6W7Y8Z`                   |
| `fin`  | txn      | `txn_01J7AB2CD3E4F5G6H7J8K9M0N1`            | `fin://txn/txn_01J7AB2CD3E4F5G6H7J8K9M0N1`                           |
| `ctxt` | snippet  | `snip_01J8PQ7R8S9T0U1V2W3X4Y5Z6A`           | `ctxt://snip/snip_01J8PQ7R8S9T0U1V2W3X4Y5Z6A`                        |

**Scope boundary**: this primitive owns only the `<typeid-canonical-string>`
segment. Scheme registration and `<entity-type>/` namespace parsing
are owned by `hop-top-uri`.

## Per-language reference implementations

These are the five SDK targets the parallel implementation tracks
will ship. Each must expose the kit API shape above, wire the
canonical string for serialisation, and add an integration test
proving cross-language string equality.

### Go — `go.jetify.com/typeid/v2`

- **Module**: `go.jetify.com/typeid/v2` (latest stable major; the
  `v1.3.0` line — last v1 release 2024-07-31 — is the fallback if a
  v2 regression blocks adoption).
- **Pin**: track latest minor of `v2`. Bump in `go.mod` via dependabot.
- **Generation**: `typeid.MustGenerate("user")` /
  `typeid.Generate("user") (TypeID, error)`.
- **Parsing**: `typeid.Parse("user_01J…")`,
  `typeid.FromUUID("user", uuidString)`.
- **Typed variant**: generic `typeid.TypeID[Prefix]` with a
  `Prefix` constraint type per entity (`type UserPrefix struct{};
  func (UserPrefix) Prefix() string { return "user" }`).
- **Serialisation**: implements `encoding.TextMarshaler` /
  `TextUnmarshaler`. `encoding/json` will use these via
  `json.Marshal` when the field type is a `TypeID`. Confirm in a
  contract test; if a future kit JSON path needs `MarshalJSON`
  explicitly, add a thin wrapper in `kit-go/core/typeid` that
  delegates to `MarshalText`.
- **SQL**: ships `Scan` / `Value` — fine for any
  `database/sql`-backed adopter.
- **Kit wrapping**: `kit-go/core/typeid` re-exports the upstream
  symbols plus a `Newtype[P]` helper that closes over a fixed prefix
  string for adopters that want one-line typed wrappers.

### Rust — `mti`

- **Crate**: `mti` v1.x on crates.io
  (<https://github.com/Govcraft/mti>). Workspace also publishes
  `typeid-prefix` and `typeid-suffix`; kit pulls `mti` only.
- **Pin**: `mti = "1.1"` initially; allow patch updates.
- **Generation**: `use mti::prelude::*; let id =
  "user".create_type_id();` (extension trait on `&str` /
  `String`).
- **Parsing**: `MagicTypeId::from_str("user_01J…")?`.
- **Typed variant**: idiomatic Rust newtype wrapping
  `MagicTypeId`:
  ```rust
  pub struct UserId(MagicTypeId);
  impl UserId {
      pub fn new() -> Self { Self("user".create_type_id()) }
  }
  ```
  Kit ships a `kit_typeid::newtype!` declarative macro to cut the
  boilerplate.
- **Serialisation**: enable the `serde` feature
  (`mti = { version = "1.1", features = ["serde"] }`).
  Serialises as the canonical string in JSON, YAML, TOML — anything
  serde supports.
- **Bus-factor flag**: `mti` currently has **one contributor**
  (`rrrodzilla`, 44 commits as of 2026-03-29) and no other
  actively-maintained Jetify-spec-compliant Rust crate exists.
  This is a known risk we accept for now. Mitigations:
  1. The dependency surface inside `kit` is small and isolated to
     `sdk/rs/kit-typeid`. If the crate goes unmaintained we can
     vendor it or swap to a replacement without touching adopters.
  2. We track upstream activity at every kit release; if there is no
     commit for 12 months and an open critical bug, the
     `kit-rs-typeid` maintainer is tasked with publishing a fork
     under the kit org (Apache-2.0 / MIT dual licence is preserved
     by the upstream).
- **Spec compliance**: README explicitly cites TypeID v0.3.0 — no
  ambiguity.

### TypeScript — `typeid-js`

- **Package**: `typeid-js` on npm
  (<https://github.com/jetify-com/typeid-js>), official jetify-com
  publication.
- **Pin**: track latest minor; semver-major only via ADR addendum.
  Requires TypeScript ≥ 5.0.
- **Generation**: `import { typeid } from 'typeid-js'; const tid =
  typeid('user');` returns `TypeID<'user'>`.
- **Parsing**: `TypeID.fromString('user_01J…', 'user')` — passing
  the expected prefix as the second arg enforces it at runtime and
  narrows the static type.
- **Typed variant**: `TypeID<Prefix extends string>` is the
  primitive branded type. For ergonomic newtypes per entity, kit
  ships type aliases:
  ```ts
  export type TaskId = TypeID<'task'>;
  export const newTaskId = (): TaskId => typeid('task');
  ```
- **String / unboxed form**: `typeid-js` also exports a
  string-based representation under `typeid-js/unboxed` for hot
  paths that don't want a class allocation. Kit defaults to the
  class form for type safety; adopters may opt into unboxed for
  perf-sensitive callsites.
- **Serialisation**: `JSON.stringify(tid)` calls `toString()` and
  emits the canonical string. `TypeID.fromString` round-trips it.
  Kit's `sdk/ts/typeid` adds a Zod schema (`z.string().refine(…)`)
  for adopters using Zod for payload validation.

### Python — `typeid-python`

- **Package**: `typeid-python` on PyPI
  (<https://github.com/akhundMurad/typeid-python>), community
  reference. Active maintenance — v0.3.10 released 2026-03-19,
  monthly cadence.
- **Pin**: `typeid-python>=0.3.10,<0.4`.
- **Generation**: `from typeid import TypeID; tid =
  TypeID(prefix="user")`.
- **Parsing**: `TypeID.from_string("user_01J…")`,
  `TypeID.from_uuid(prefix="user", suffix=uuid7_value)`.
- **Typed variant**: `TypeID[Literal["user"]]` plus the
  `typeid_factory` helper:
  ```python
  from typing import Literal
  from typeid import TypeID, typeid_factory

  UserID = TypeID[Literal["user"]]
  new_user_id = typeid_factory("user")
  ```
- **Serialisation**: Pydantic v2 integration via `TypeIDField` for
  adopters using Pydantic models. For plain JSON, `str(tid)` is
  the canonical form; kit's `sdk/py/typeid` exposes a
  `to_json` / `from_json` pair that simply wraps `str()` and
  `TypeID.from_string`.
- **Rust-accelerated**: upstream pulls a Rust base32 backend
  transparently — no extra step for adopters.

### PHP — `jewei/typeid-php` (recommended) or community impl TBD

- **Status**: PHP has **no official jetify-com SDK**. Two community
  candidates surfaced:
  1. `jewei/typeid-php` (PHP 8.4, packagist-published, last update
     2026-04-30, zero runtime deps, MIT licence).
  2. `paper-co/typeid-php-package` (last update 2025-03-16, less
     active, single contributor, no recent CI signal).
- **Recommendation**: adopt `jewei/typeid-php` **provisionally**,
  pending acceptance review against the criteria below. If the
  review fails, this slot becomes **TBD** and `sdk/php/kit-typeid`
  ships a thin in-tree implementation behind the same kit API
  surface until a stable upstream emerges.
- **Pin**: `composer require jewei/typeid-php:^1.0` once
  acceptance review passes (current packagist version stream
  visible from the badge in the upstream README).
- **Acceptance criteria** (any failure → in-tree fork):
  - Implements spec **v0.3.0** (prefix grammar, base32 encoding,
    UUIDv7 default).
  - Passes the published `spec/valid.json` and `spec/invalid.json`
    conformance vectors from the jetify-com/typeid repo. We add a
    PHP runner in `sdk/php/kit-typeid/tests/spec_conformance/`.
  - Round-trips with the Go, TS, Python, and Rust references over
    a 10k-sample shared fixture (committed to `contracts/typeid/`).
  - Active maintenance: at least one release in the last 6 months
    OR responsive maintainer on a kit-filed test issue.
  - Apache-2.0, MIT, or BSD licence — `jewei/typeid-php` is MIT,
    which is fine.
- **Generation / parsing** (mirroring the rest):
  ```php
  use TypeID\TypeID;
  $id = TypeID::generate('user');           // user_01J…
  $id = TypeID::fromString('user_01J…');
  $id = TypeID::fromUuid($uuid, 'invoice');
  ```
- **Typed variant**: PHP has no generics; kit ships a
  `TypedTypeId` abstract base that adopters subclass per entity
  (`class TaskId extends TypedTypeId { protected const PREFIX = 'task'; }`).
- **Serialisation**: `JsonSerializable` implementation on the
  primitive returning the canonical string. Kit wires this in
  `sdk/php/kit-typeid` if upstream omits it.

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
- PHP has no official upstream; carries a non-trivial review
  burden and possibly an in-tree fork.
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
  pkg: `go.jetify.com/typeid/v2`.
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
