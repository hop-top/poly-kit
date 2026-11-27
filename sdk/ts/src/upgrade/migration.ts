/**
 * Versioned schema migrations and the registry that holds them, ported
 * from Go `core/upgrade`. The registry selects what is pending between two
 * versions; running them is the Migrator's job.
 */

import { compareSemver, parseSemver, trimV } from './semver';

/** A versioned schema migration. */
export interface Migration {
  /** Semver, pre-release allowed. A leading `v` is dropped when ordering. */
  version: string;
  /** Matches the schema driver's name. */
  schema: string;
  up(signal?: AbortSignal): void | Promise<void>;
  /** Absent means no rollback. */
  down?(signal?: AbortSignal): void | Promise<void>;
  /**
   * When set, runs after `up` and before the version is recorded. A failure
   * counts as a failed step and the rollback includes this migration.
   */
  verify?(signal?: AbortSignal): void | Promise<void>;
}

/**
 * Holds migrations for one or more Migrators. {@link registerMigration}
 * feeds {@link defaultRegistry}; a Migrator reads that one unless given
 * another, made with `new Registry()`.
 */
export class Registry {
  private migrations: Migration[] = [];

  /** Adds a migration. Throws a TypeError when `up` is not a function. */
  register(m: Migration): void {
    if (typeof m.up !== 'function') {
      throw new TypeError(`upgrade: migration ${m.version} has nil Up function`);
    }
    this.migrations.push(m);
  }

  /** Empties the registry. */
  reset(): void {
    this.migrations = [];
  }

  /**
   * Migrations of `schema` whose version v satisfies from < v <= to,
   * ascending by version, versions as registered. Equal versions have no
   * defined order.
   */
  pending(schema: string, from: string, to: string): Migration[] {
    const fromV = parseSemver(trimV(from));
    const toV = parseSemver(trimV(to));
    const out: { m: Migration; v: ReturnType<typeof parseSemver> }[] = [];
    for (const m of this.migrations) {
      if (m.schema !== schema) continue;
      const v = parseSemver(trimV(m.version));
      if (compareSemver(v, fromV) > 0 && compareSemver(v, toV) <= 0) out.push({ m, v });
    }
    out.sort((a, b) => compareSemver(a.v, b.v));
    return out.map((e) => e.m);
  }
}

const defaultReg = new Registry();

/** The registry {@link registerMigration} adds to. */
export function defaultRegistry(): Registry {
  return defaultReg;
}

/** Adds a migration to the default registry. */
export function registerMigration(m: Migration): void {
  defaultReg.register(m);
}

/** Clears the default registry. For tests only. */
export function resetRegistryForTest(): void {
  defaultReg.reset();
}
