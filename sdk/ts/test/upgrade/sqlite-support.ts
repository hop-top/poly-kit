// Helpers of the sqlite driver tests. Loads better-sqlite3: import only
// from `*.integration.test.ts`.

import Database from 'better-sqlite3';

/**
 * Opens (creating) a WAL-mode database with table t(x). Auto-checkpoint is
 * off and the handle stays open, so committed rows stay in the -wal file:
 * a plain copy of the database file does not hold them.
 */
export function openWAL(path: string): Database.Database {
  const db = new Database(path);
  db.pragma('journal_mode = WAL');
  db.pragma('wal_autocheckpoint = 0');
  db.exec('CREATE TABLE IF NOT EXISTS t (x INTEGER)');
  return db;
}

export function insert(db: Database.Database, ...xs: number[]): void {
  const stmt = db.prepare('INSERT INTO t (x) VALUES (?)');
  for (const x of xs) stmt.run(x);
}

/** Reads t(x) in order on a connection of its own, closed after. */
export function rows(path: string): number[] {
  const db = new Database(path, { readonly: true, fileMustExist: true });
  try {
    return (db.prepare('SELECT x FROM t ORDER BY x').all() as { x: number }[]).map((r) => r.x);
  } finally {
    db.close();
  }
}

export function integrity(path: string): string {
  const db = new Database(path, { readonly: true, fileMustExist: true });
  try {
    return db.pragma('integrity_check', { simple: true }) as string;
  } finally {
    db.close();
  }
}
