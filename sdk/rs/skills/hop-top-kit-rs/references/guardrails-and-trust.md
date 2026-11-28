# Guardrails and trust

Open when limiting, redacting, consenting, signing or migrating. Long form:
[Rust SDK reference](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/rs-sdk.md#identity).

| Need | Feature | Notes |
|------|---------|-------|
| Circuit breaker, MaxOps and MaxBytes, rate limit | `breaker` | wasm-safe with a caller clock |
| Secret and PII redaction | `redact` | wasm-safe |
| Per-capability consent grants | `consent`; `consent-file` | files are native |
| Dry-run and fake side-effects (fs, http, bus, exec) | `sideeffect`; `sideeffect-real`, `sideeffect-http` | the `--dry-run` flag itself is not wired |
| Local Ed25519 identity, JWT, secretbox | `identity` | seeds and nonces from the caller on wasm32 |
| Declarative CEL guard rules | `policy` | wasm-safe |
| TypeIDs | `id` | prefixed identifiers |
| State migrations with backup and rollback | `upgrade`; `upgrade-sqlite`, `upgrade-fsdir`, `upgrade-postgres` | see below |
| Retry with jitter | no feature needed | `util::retry`, pure std |

Migrations: pick the driver for where the state lives (file: `upgrade`
alone; SQLite file or handle: `upgrade-sqlite`; a directory:
`upgrade-fsdir`; shared Postgres: `upgrade-postgres` over your own
`postgres::Client`). Drivers lock so two processes cannot migrate one store.

Gaps taken from the parity page:

- Side-effect seams: the `--dry-run` flag and per-command tiers are not wired.
- Identity: `cli.WithIdentity` is not ported.
- Upgrade and migrations: migrations and the release check only; download and
  binary replace are not ported, nor a migrate command tree or a TiDB driver.
- Utilities: retry only.

Path scope, operating mode, provenance and the audit log have no Rust
module: [other ports](other-ports.md).
