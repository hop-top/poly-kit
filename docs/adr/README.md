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
| 0005 | [Output column precedence and header/key identity](./0005-output-column-precedence-and-header-key-identity.md) <a id="0005-output-column-precedence-and-header-key-identity"></a> | Accepted | `--cols` reorders as well as selects and the user's order wins; `header == key` universally, because Go cannot express a split via `table:""` tags. |

## Claiming a number

The highest number on `main` is **not** the highest number claimed —
in-flight branches hold numbers that `main` cannot see. Two branches
that both read `main` will otherwise pick the same next number and
collide at merge.

To find a genuinely free number:

```sh
git fetch --all
make lint-adr-numbers   # reports the highest number across all refs
```

`make lint-adr-numbers` also runs in CI (`adr-numbers-lint`) and fails
the build when one number maps to two different filenames on any two
refs, naming both files. If it fails on your PR, someone claimed your
number first: rename your file to the next free number and update the
index row below in the same commit.

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
