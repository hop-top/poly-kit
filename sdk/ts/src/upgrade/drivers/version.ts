/** Where a version-file driver reads its version, shared by the drivers here. */

import { defaultDataDir, readVersionFile } from '../backup';

/** Options every driver in this directory accepts. */
export interface DriverOptions {
  /** Reads the version file under this dir, for a Migrator built with the same `dataDir`. */
  dataDir?: string;
  /** Tool name for the default data dir `<xdg data dir>/hop/<tool>`. */
  tool?: string;
}

/** The data dir the Migrator keeps `<schema>/version` and backups in. */
export function resolveDataDir(opts: DriverOptions): string {
  return opts.dataDir !== undefined && opts.dataDir !== '' ? opts.dataDir : defaultDataDir(opts.tool ?? '');
}

/** The version the Migrator recorded for `schema`; `""` when none. */
export function versionOf(opts: DriverOptions, schema: string): Promise<string> {
  return readVersionFile(resolveDataDir(opts), schema);
}
