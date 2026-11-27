/**
 * The seam between the Migrator and a data store, ported from Go
 * `core/upgrade` `SchemaDriver`.
 *
 * Every method may return a value or a Promise: a driver for a server
 * database is async, one over `better-sqlite3` or a file is sync, and the
 * Migrator awaits either. Capabilities are optional members the Migrator
 * detects by shape, so a later driver adds one without any change to the
 * Migrator: `lock` (a method) and `storesVersion` (a flag).
 */

type MaybePromise<T> = T | Promise<T>;

/** Releases a lock taken by {@link SchemaDriver.lock}. */
export type Unlock = () => MaybePromise<void>;

export interface SchemaDriver {
  /** The schema identifier, matched against `Migration.schema`. */
  name(): string;

  /** The applied version, or `""` when none is stored. */
  version(): MaybePromise<string>;

  /**
   * Records a version: after each applied migration, and the earlier one
   * again after a rollback or restore. For a driver without
   * `storesVersion`, called after the Migrator wrote the version file.
   */
  setVersion(version: string): MaybePromise<void>;

  /**
   * Copies the current state into the existing directory `dest`. Resolve
   * only when it really did; throw {@link BackupUnsupportedError} (or an
   * error whose cause chain has one) when it cannot; any other throw is a
   * failure that stops the run.
   */
  backup(dest: string): MaybePromise<void>;

  /**
   * Replaces the current state from the backup directory `src`. Same
   * contract as `backup`: resolve only when it really restored.
   */
  restore(src: string): MaybePromise<void>;

  /**
   * Optional, for a store several processes can migrate at once. The
   * Migrator calls it before it reads the version and holds the lock for
   * the whole run of that schema (version read, baseline, backup, every
   * `up`, every version record, rollback, prune), then calls the returned
   * unlock, on every path.
   *
   * Wait until the lock is acquired or `signal` aborts; in the second
   * case reject with `abortError(signal)` and hold nothing. A rejection
   * fails the schema before anything else (`lock: <message>`, no unlock).
   * An unlock failure is only a warning. `RollbackLatest` locks too;
   * `status` never does.
   */
  lock?(signal?: AbortSignal): MaybePromise<Unlock>;

  /**
   * Optional: `true` when the driver keeps its version itself (a database
   * table). The Migrator then calls only `setVersion` to record a version
   * and writes no version file; `version()` is the one authority.
   */
  readonly storesVersion?: boolean;
}
