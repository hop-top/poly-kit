// Child process of the Postgres race test: one Migrator over its own
// connection pool. Applies three migrations, each appending one line to a
// shared file, so the parent can count how often each ran. Starts when the
// parent sends "go".

import { appendFileSync } from 'node:fs';

import { Pool } from 'pg';

import { Migrator } from '../../src/upgrade/migrator';
import { Registry } from '../../src/upgrade/migration';
import { openPostgresDriver } from '../../src/upgrade/drivers/postgres';

const { PGCFG, TABLE, SCHEMA, COUNTS, DATA_DIR } = process.env as Record<string, string>;

async function main(): Promise<void> {
  const pool = new Pool({ ...JSON.parse(PGCFG), max: 3 });
  const reg = new Registry();
  for (const v of ['1.1.0', '1.2.0', '1.3.0']) {
    reg.register({
      version: v,
      schema: SCHEMA,
      up: async () => {
        appendFileSync(COUNTS, `${process.pid} ${v}\n`);
        await new Promise((r) => setTimeout(r, 60)); // widen the race window
      },
    });
  }
  const m = new Migrator('racetest', '1.3.0', { registry: reg, dataDir: DATA_DIR });
  m.addDriver(await openPostgresDriver({ db: pool, connect: () => pool.connect(), table: TABLE, name: SCHEMA }));
  await new Promise<void>((resolve) => process.once('message', () => resolve()));
  process.send?.('started');
  try {
    await m.run();
    process.send?.('ok');
  } catch (e) {
    process.send?.(`error ${(e as Error).message}`);
  }
  await pool.end();
}

main().then(
  () => process.exit(0),
  (e) => {
    process.send?.(`error ${(e as Error).message}`);
    process.exit(1);
  },
);
