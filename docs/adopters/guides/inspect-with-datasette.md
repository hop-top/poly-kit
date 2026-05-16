# Inspect a kit instance with Datasette

Browse, query, and export the SQLite state of a running or stopped
`kit serve` instance using [Datasette](https://datasette.io/) — a
read-only web UI and JSON API over any SQLite file.

## Who this is for

Operators debugging a kit-powered product, engineers verifying a
migration, anyone who needs to answer "what's actually in the DB
right now?" without writing SQL by hand or reaching for `sqlite3`.

This is a recipe, not a kit feature — Datasette is an external tool
you install once and point at any kit instance's data directory. Kit
ships a metadata file and a few canned queries to make the experience
kit-aware.

## When to use it

- **Operational debugging** — "show me every event from the breaker
  in the last hour, joined to the document it tripped on"
- **Audit-trail review** — query the version DAG to see when and how
  a document changed
- **Sharing slices of state** — hand a stakeholder a URL filtered to
  the rows they need, with PII columns masked
- **Verifying a migration or a backfill** — diff before and after as
  CSV; check counts, ranges, nullability

When **not** to use it:

- Hot OLTP queries (Datasette opens read-only and is not for high-QPS reads)
- Modifying data — Datasette is read-only by design
- Replacing the engine HTTP API — that's `kit serve` and the SDKs

## Before you begin

You need:

- A kit instance with a SQLite data directory you can read. Default
  location: per-instance, set by `--data` on `kit serve`.
- Datasette installed. Recommended:

  ```bash
  uv tool install datasette        # uv
  pipx install datasette            # pipx
  pip install --user datasette      # last resort
  ```

  Datasette is Python; if your kit deployment doesn't have Python
  available, run Datasette on a different machine and copy the DB
  file (see [Copying a live DB safely](#copying-a-live-db-safely)).

- Optional plugins worth installing once:

  ```bash
  datasette install datasette-mask    # column-level masking
  datasette install datasette-pretty-json
  datasette install datasette-vega    # quick charts
  ```

## Quick start

1. **Locate the data directory.** For a default `kit serve`:

   ```bash
   kit config get serve.data-dir
   # or, equivalently:
   ls "$(kit config get serve.data-dir)"
   ```

2. **Run Datasette in read-only mode** against the SQLite files:

   ```bash
   datasette serve \
     "$(kit config get serve.data-dir)/data.db" \
     --immutable \
     --metadata examples/datasette/kit-metadata.json \
     --port 8001
   ```

   `--immutable` opens the DB without any locks, safe to run against
   a live `kit serve` writer. `--metadata` points at the kit-aware
   metadata file documented below.

3. **Open the browser** at <http://localhost:8001>. You'll see the
   `documents`, `versions`, `version_parents`, and `snapshots` tables
   (after the `engine-versioned-sqlite` track lands; before that,
   only `documents`).

## Kit-aware metadata

The shipped metadata file at `examples/datasette/kit-metadata.json`
configures Datasette with kit's table conventions:

- Human-readable table descriptions
- Faceted browsing on `type`, `source`, and similar high-cardinality
  routing columns
- Column-level masking on fields that are likely to carry PII
  (configurable; see [Redaction](#redaction))
- Canned queries for the most common kit inspection patterns

You can copy and adapt this file per environment.

### Example metadata structure

```json
{
  "title": "kit instance — production",
  "databases": {
    "data": {
      "tables": {
        "documents": {
          "description": "Type-tagged JSON documents. (type, id) is the primary key.",
          "facets": ["type"],
          "sortable_columns": ["created_at", "updated_at", "type", "id"]
        },
        "versions": {
          "description": "Persistent version DAG (engine-versioned-sqlite). seq is monotonic per (type, id).",
          "facets": ["type"],
          "sortable_columns": ["created_at", "seq", "type", "id"]
        },
        "snapshots": {
          "description": "Full JSON payload per version_id. Joined to versions on version_id.",
          "hidden": false
        },
        "version_parents": {
          "description": "Parent edges in the version DAG. Schema supports branching even though the public API appends linearly.",
          "hidden": false
        }
      },
      "queries": {
        "recent-mutations": {
          "title": "Recent mutations across all types",
          "sql": "select v.type, v.id, v.seq, v.created_at, length(s.data) as bytes\nfrom versions v join snapshots s using (version_id)\norder by v.created_at desc\nlimit 200"
        },
        "history-for-document": {
          "title": "Full history for one document",
          "sql": "select v.seq, v.version_id, v.created_at, v.hash, length(s.data) as bytes\nfrom versions v join snapshots s using (version_id)\nwhere v.type = :type and v.id = :id\norder by v.seq",
          "params": ["type", "id"]
        },
        "branched-documents": {
          "title": "Documents whose DAG has branched (multiple heads)",
          "sql": "with heads as (\n  select version_id from versions\n  where version_id not in (select parent_id from version_parents)\n)\nselect v.type, v.id, count(*) as head_count\nfrom versions v join heads using (version_id)\ngroup by v.type, v.id\nhaving count(*) > 1"
        }
      }
    }
  }
}
```

Save under `examples/datasette/kit-metadata.json` so it ships with
the repo.

## Querying bus events

If your `kit serve` is configured with a `bus.JSONLSink` writing
events to a file (typical for audit/observability setups), pipe the
JSONL into a SQLite table for queryable history:

```bash
datasette insert events.db bus_events --jsonl < /var/log/kit/bus.jsonl
datasette serve events.db data.db --immutable
```

Now bus events are joinable against documents in the same Datasette
instance. Common query: "every event from `kit.runtime.breaker.*`
in the last 24h, with the document context that tripped it."

## Redaction

Datasette is read-only but is not a redaction tool by itself.
Sensitive columns leak unless you configure masking.

**Recommended pattern**: use the `datasette-mask` plugin with kit's
redact rules expressed as column patterns. The plugin masks values
in the UI and in the JSON API.

```json
{
  "plugins": {
    "datasette-mask": {
      "tables": {
        "documents": {
          "data": {
            "patterns": ["api_key", "secret", "password", "token", "email"],
            "replacement": "[redacted]"
          }
        }
      }
    }
  }
}
```

For deployments where kit's redact rules already exist, mirror them
into the Datasette metadata at deploy time. A future `kit inspect`
subcommand (see [Future work](#future-work)) would generate this
mirror automatically.

> **Trust boundary:** Datasette has no notion of kit's redact
> policies. Anyone who can reach the Datasette URL can read every
> column of every table that isn't masked. **Do not run Datasette
> exposed publicly** without authentication. Use the
> `datasette-auth-passwords` or `datasette-auth-existing-cookies`
> plugins, or front it with a reverse proxy that handles auth.

## Copying a live DB safely

If you can't run Datasette on the same machine as `kit serve`:

```bash
# Use SQLite's online backup; safe against a live writer.
sqlite3 "$(kit config get serve.data-dir)/data.db" \
  ".backup '/tmp/kit-snapshot.db'"

scp /tmp/kit-snapshot.db ops-host:/tmp/
ssh ops-host datasette serve /tmp/kit-snapshot.db --immutable
```

`.backup` produces a transactionally consistent copy without
blocking writers. Don't `cp` a SQLite file in WAL mode — you'll get
an inconsistent snapshot.

## Performance and safety notes

- **`--immutable`** opens the DB read-only with no locks. Safe to run
  alongside an active `kit serve` writer.
- **Datasette's query timeout** defaults to 1 second; bump with
  `--setting sql_time_limit_ms 10000` for ad-hoc analytics. Keep it
  bounded — you do not want a runaway query saturating the
  read-replica IO.
- **Table size**: Datasette pages results. Large tables work fine
  but full-table downloads can be slow; prefer canned queries with
  `LIMIT`.
- **Joins across DBs**: pass multiple DB files to a single
  `datasette serve` invocation; Datasette can join them.

## Future work

A `kit inspect` subcommand would wrap this recipe behind a
kit-native invocation:

```bash
kit inspect            # auto-discovers data dir, opens browser
kit inspect --port 8001
kit inspect --redact strict   # mirrors kit's redact rules into metadata
```

Tracked separately. Until then, this recipe is the canonical path.

## See also

- [Datasette docs](https://docs.datasette.io/)
- [`docs/adopters/concepts/engine-overview.md`](../concepts/engine-overview.md) — what `kit serve` writes to disk
- [`docs/contributors/specs/engine-store-versioned-sqlite.md`](../../contributors/specs/engine-store-versioned-sqlite.md) — schema for `versions`, `version_parents`, `snapshots`
- [`docs/contributors/adr/0010-sqlite-engine-choice.md`](../../contributors/adr/0010-sqlite-engine-choice.md) — the SQLite engine kit uses (Datasette works against any of them)
- [`docs/adopters/guides/inspect-config-paths.md`](inspect-config-paths.md) — sibling debugging recipe for kit configuration
