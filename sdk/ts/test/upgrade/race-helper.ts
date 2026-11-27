// The second process of the sqlite two-process test, bundled by the test
// with esbuild and run with node. Migrates `<RACE_DIR>/app.db` from 1.0.0
// to 1.2.0, each migration recording its version and pid in `applied`.

import Database from 'better-sqlite3';

import { Migrator, Registry } from '../../src/upgrade';
import { SqliteDriver } from '../../src/upgrade/drivers/sqlite';

const dir = process.env['RACE_DIR'] as string;
const dbPath = `${dir}/app.db`;
const data = `${dir}/data`;
const db = new Database(dbPath);

const reg = new Registry();
for (const v of ['1.1.0', '1.2.0']) {
  reg.register({
    version: v,
    schema: 'appdb',
    up: async () => {
      await new Promise((r) => setTimeout(r, 200)); // a window for the other process
      db.prepare('INSERT INTO applied (version, pid) VALUES (?, ?)').run(v, process.pid);
    },
  });
}
const m = new Migrator('tool', '1.2.0', { registry: reg, dataDir: data });
m.addDriver(new SqliteDriver(dbPath, { name: 'appdb', dataDir: data }));
m.run().then(
  () => {
    db.close();
  },
  (e: unknown) => {
    console.error(e);
    process.exit(1);
  },
);
