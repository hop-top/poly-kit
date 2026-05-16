/**
 * @module sqlstore
 *
 * SQLite-backed key-value store with optional TTL and custom migration support.
 *
 * ## Schema
 * ```sql
 * CREATE TABLE IF NOT EXISTS kv (
 *   key        TEXT PRIMARY KEY,
 *   value      TEXT NOT NULL,
 *   expires_at INTEGER          -- NULL means no expiry; epoch-ms when set
 * )
 * ```
 *
 * ## TTL semantics
 * - `opts.ttl` is in **milliseconds**.
 * - On `put`, `expires_at` is set to `Date.now() + ttl` when `ttl > 0`.
 * - On `get`, if `expires_at !== null && expires_at <= Date.now()`, `null` is returned.
 *   Expired rows are NOT automatically deleted; prune them with:
 *   ```sql
 *   DELETE FROM kv WHERE expires_at IS NOT NULL AND expires_at <= <now>
 *   ```
 *
 * ## migrateSql
 * If `opts.migrateSql` is provided it is executed once inside `open()`, after
 * the `kv` table is created.  Use it to add application-specific tables,
 * indexes, or seed data.  Multiple statements must be separated by semicolons.
 *
 * ## Example
 * ```ts
 * import { open } from '@hop-top/kit/sqlstore'
 *
 * const store = open('/tmp/myapp.db', { ttl: 60_000 })
 * store.put('session:abc', { userId: 1 })
 * const session = store.get<{ userId: number }>('session:abc')
 * store.close()
 * ```
 */

import Database from 'better-sqlite3'

/** Options accepted by {@link open}. */
export interface Options {
  /**
   * Time-to-live in **milliseconds**. When positive, values older than this
   * are treated as absent on `get`. Zero or omitted disables expiry.
   */
  ttl?: number

  /**
   * SQL executed once during `open()` after the `kv` table is created.
   * Useful for adding custom tables, indexes, or initial data.
   * Multiple statements must be semicolon-separated.
   */
  migrateSql?: string
}

/** A SQLite-backed key-value store. */
export interface Store {
  /**
   * Serialises `value` as JSON and upserts it under `key`.
   * If `key` already exists its value (and expiry) are overwritten.
   */
  put(key: string, value: unknown): void

  /**
   * Returns the JSON-deserialised value for `key`, or `null` if the key
   * does not exist or has expired.
   */
  get<T>(key: string): T | null

  /** Returns the underlying `better-sqlite3` Database instance. */
  db(): Database.Database

  /** Closes the underlying database connection. */
  close(): void
}

const CREATE_KV = `
  CREATE TABLE IF NOT EXISTS kv (
    key        TEXT    PRIMARY KEY,
    value      TEXT    NOT NULL,
    expires_at INTEGER
  )
`

/**
 * Opens (or creates) a SQLite database at `path`, ensures the `kv` table
 * exists, and runs `opts.migrateSql` if supplied.
 *
 * Pass `':memory:'` as `path` for an in-memory database (useful in tests).
 *
 * @param path       Filesystem path or `':memory:'`.
 * @param opts       Optional TTL and migration SQL.
 */
export function open(path: string, opts: Options = {}): Store {
  const database = new Database(path)

  // Create kv table
  database.exec(CREATE_KV)

  // Run caller-supplied migration
  if (opts.migrateSql) {
    database.exec(opts.migrateSql)
  }

  const stmtPutNoExpiry = database.prepare(
    `INSERT INTO kv (key, value, expires_at) VALUES (?, ?, NULL)
     ON CONFLICT(key) DO UPDATE SET value = excluded.value, expires_at = NULL`,
  )

  const stmtPutWithExpiry = database.prepare(
    `INSERT INTO kv (key, value, expires_at) VALUES (?, ?, ?)
     ON CONFLICT(key) DO UPDATE SET value = excluded.value, expires_at = excluded.expires_at`,
  )

  const stmtGet = database.prepare(`SELECT value, expires_at FROM kv WHERE key = ?`)

  return {
    put(key: string, value: unknown): void {
      const json = JSON.stringify(value)
      if (opts.ttl && opts.ttl > 0) {
        stmtPutWithExpiry.run(key, json, Date.now() + opts.ttl)
      } else {
        stmtPutNoExpiry.run(key, json)
      }
    },

    get<T>(key: string): T | null {
      const row = stmtGet.get(key) as { value: string; expires_at: number | null } | undefined
      if (row == null) return null
      if (row.expires_at !== null && row.expires_at <= Date.now()) return null
      return JSON.parse(row.value) as T
    },

    db(): Database.Database {
      return database
    },

    close(): void {
      database.close()
    },
  }
}
