/** Options and result types of the {@link Migrator}. */

import type { EventPublisher, MigrationTopics } from './events';
import type { Registry } from './migration';

/** Options of a {@link Migrator}. */
export interface MigratorOptions {
  /** Disables automatic rollback on failure; enables `rollbackLatest`. */
  manualRollback?: boolean;
  /** Recent backups kept per schema (default 5). */
  backupRetention?: number;
  /** Migrations to read instead of the default registry. */
  registry?: Registry;
  /**
   * Where version files and backups live instead of
   * `<xdg data dir>/hop/<tool>`. Drivers that read the version file need
   * the same dir (`readVersionFile`).
   */
  dataDir?: string;
  /**
   * Checks each schema's final state after its last pending migration,
   * once that migration's version is recorded. A failure rolls back every
   * migration applied to that schema in the run.
   */
  verify?: (schema: string, version: string, signal?: AbortSignal) => void | Promise<void>;
  /**
   * What an unversioned store is already at. Called when a driver's
   * `version()` is `""`, before pending selection; a non-empty result is
   * recorded like any version (event `baselined`) and the migrations at or
   * below it are not applied. An error fails that schema's run
   * (`baseline: ...`) before any backup or `up`. Never called when a
   * version is stored, when reading it failed, or by `status`.
   */
  baseline?: (schema: string, signal?: AbortSignal) => string | undefined | Promise<string | undefined>;
  /** Receives the lifecycle events (a kit `Bus` fits). Without it a Migrator is silent. */
  publisher?: EventPublisher;
  /** Replaces individual topics; others keep their `kit.core.migration.*` value. */
  topics?: Partial<MigrationTopics>;
  /** Sets every topic from a 3-segment prefix (`myapp.core.migration`); `topics` wins per field. */
  topicPrefix?: string;
  /** Receives warnings (an unlock or a prune that failed). Default `console.warn`. */
  warn?: (message: string) => void;
}

/** A successfully applied migration. */
export interface AppliedMigration {
  version: string;
  schema: string;
}

/** Migration state of one schema. */
export interface SchemaStatus {
  schema: string;
  current: string;
  target: string;
  pending: number;
  /** Set when the driver's `version()` failed; `current` is then `""`. */
  error?: Error;
}
