# architecture

How the kit codebase is organised: domains, layering, dependency rules, where new code belongs.

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`architecture.md`](architecture.md) | concept page on how kit is structured and what each domain owns | you decide where new code belongs, or want a mental model before browsing packages |
| [`engine-sdk-parity.md`](engine-sdk-parity.md) | proposed policy for the PHP and Rust engine clients: reference clients, surface to port, wire-compat test, idioms, publishing | you add or review an engine client for another language |
| [`bus-streams.md`](bus-streams.md) | design for bus network streams: capability advertisement, remote subscribe, resume offsets, reset, offline outbox | you change the bus network wire protocol or port it to another SDK |
| [`upgrade-sql-backends.md`](upgrade-sql-backends.md) | design for `core/upgrade` migrations on SQL backends: version authority, per-step recording, baseline, locking, backup honesty, handles, and how it differs from `storage/sqldb` | you add or change a migration driver, the Migrator's run sequence, or port `core/upgrade` to another SDK |
