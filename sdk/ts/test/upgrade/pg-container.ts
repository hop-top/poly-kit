// A throwaway Postgres 16 container for the Postgres driver's integration
// tests: a unique name, a random loopback port, removed on stop. A missing
// or failing Docker makes start() throw, so the suite fails instead of
// skipping.

import { execFileSync, spawnSync } from 'node:child_process';
import { randomBytes } from 'node:crypto';

import { Client } from 'pg';

export interface PgContainer {
  name: string;
  /** Connection config of the container's `testdb`. */
  config: { host: string; port: number; user: string; password: string; database: string };
  stop(): void;
}

const IMAGE = 'postgres:16-alpine';
const READY = 'database system is ready to accept connections';

const docker = (...args: string[]): string =>
  execFileSync('docker', args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim();

const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

export async function startPostgres(): Promise<PgContainer> {
  const name = `kit-ts-pg-upgrade-${process.pid}-${randomBytes(3).toString('hex')}`;
  try {
    docker(
      'run', '-d', '--rm', '--name', name,
      '-e', 'POSTGRES_USER=test', '-e', 'POSTGRES_PASSWORD=test', '-e', 'POSTGRES_DB=testdb',
      '-p', '127.0.0.1::5432', IMAGE,
    );
  } catch (e) {
    throw new Error(`postgres container: ${(e as Error).message} (docker is required for these tests)`);
  }
  const stop = (): void => {
    spawnSync('docker', ['rm', '-f', name], { stdio: 'ignore' });
  };
  try {
    const port = Number(docker('port', name, '5432/tcp').split('\n')[0].split(':').pop());
    const config = { host: '127.0.0.1', port, user: 'test', password: 'test', database: 'testdb' };
    // The image restarts the server once after initdb: ready is the second line.
    for (let i = 0; i < 240; i++) {
      const logs = spawnSync('docker', ['logs', name], { encoding: 'utf8' });
      const n = ((logs.stdout ?? '') + (logs.stderr ?? '')).split(READY).length - 1;
      if (n >= 2) {
        const c = new Client(config);
        try {
          await c.connect();
          await c.end();
          return { name, config, stop };
        } catch {
          // not accepting yet
        }
      }
      await sleep(500);
    }
    throw new Error('postgres container did not become ready in 120s');
  } catch (e) {
    stop();
    throw e;
  }
}
