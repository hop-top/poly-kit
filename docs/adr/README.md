# Architecture Decision Records

Cross-cutting decisions that bind kit's polyglot surface (Go primary;
TS, Python, Rust, PHP SDKs). One ADR per decision; superseded ADRs
are kept in place for history.

## Index

| ID   | Title                                                                                                | Status   | Summary                                                                                          |
| ---- | ---------------------------------------------------------------------------------------------------- | -------- | ------------------------------------------------------------------------------------------------ |
| 0001 | [TypeID as kit's entity-ID primitive](./0001-typeid-primitive.md) <a id="0001-typeid-primitive"></a> | Accepted | Adopt Jetify TypeID v0.3.0 as the canonical wire format for entity IDs across all kit bindings. |

## Conventions

- **Filename**: `NNNN-kebab-title.md` (zero-padded 4-digit sequence).
- **Status** values: `Proposed`, `Accepted`, `Superseded by NNNN`,
  `Deprecated`.
- **Required sections**: Status, Date, Context, Decision,
  Consequences. See `0001-typeid-primitive.md` for the reference
  shape.
- **Refs**: link the originating `tlc/` track or task in the header
  block.
