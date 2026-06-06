# Architecture Decision Records

Cross-cutting decisions that bind kit's polyglot surface (Go primary;
TS, Python, Rust, PHP SDKs). One ADR per decision; superseded ADRs
are kept in place for history.

## Index

| ID   | Title                                                                                                | Status   | Summary                                                                                          |
| ---- | ---------------------------------------------------------------------------------------------------- | -------- | ------------------------------------------------------------------------------------------------ |
| 0001 | [TypeID as kit's entity-ID primitive](./0001-typeid-primitive.md) <a id="0001-typeid-primitive"></a> | Accepted | Adopt Jetify TypeID v0.3.0 as the canonical wire format for entity IDs across all kit bindings. |
| 0002 | [LLM pool routing primitives](./0002-llm-pool-routing-primitives.md) <a id="0002-llm-pool-routing-primitives"></a> | Accepted | Ship a deterministic LLM picker + categorical `BudgetTier` + operator pool gating in `go/ai/llm/`, delegating model metadata to `hop.top/aim`. |
| 0003 | [uri + hdl consolidated into cite](./0003-cite-consolidates-uri-and-hdl.md) <a id="0003-cite-consolidates-uri-and-hdl"></a> | Accepted | Replace `hop.top/uri` with `hop.top/cite v0.1.0` as the canonical poly-URI library; drop orphan `hop.top/hdl` (already de-replaced). |

## Conventions

- **Filename**: `NNNN-kebab-title.md` (zero-padded 4-digit sequence).
- **Status** values: `Proposed`, `Accepted`, `Superseded by NNNN`,
  `Deprecated`.
- **Required sections**: Status, Date, Context, Decision,
  Consequences. See `0001-typeid-primitive.md` for the reference
  shape.
- **Refs**: optional; link external references (specs, upstream
  issues, vendor docs) when relevant. Do NOT cite internal task
  tracker IDs — repo artifacts never reference internal context.
- **Acknowledged quirks**: when the decision ships with magic numbers,
  upstream gotchas, or operator-facing edges, include a section that
  names them. See ADR 0002 for the pattern.
