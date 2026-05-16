import { describe, it, expect } from 'vitest';
import * as path from 'path';
import { load } from '../../../sdk/ts/src/config';
import { createBus, createEvent, type Event } from '../../../sdk/ts/src/bus';
import { ProgressReporter } from '../../../sdk/ts/src/progress';
import { checkTargets, type ProbeConfig } from './core';
import { createServer, type Server } from 'http';

function startMockServer(
  statusCode: number,
): Promise<{ server: Server; url: string }> {
  return new Promise((resolve) => {
    const server = createServer((_, res) => {
      res.writeHead(statusCode);
      res.end();
    });
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      if (addr && typeof addr === 'object') {
        resolve({ server, url: `http://127.0.0.1:${addr.port}` });
      }
    });
  });
}

describe('probe config loading', () => {
  it('loads probe.yaml', () => {
    const cfg: ProbeConfig = { interval: '', targets: [] };
    const cfgPath = path.resolve(__dirname, '..', 'probe.yaml');
    load(cfg, { projectConfigPath: cfgPath });
    expect(cfg.targets.length).toBeGreaterThan(0);
    expect(cfg.interval).toBe('30s');
  });
});

describe('checkTargets', () => {
  it('emits kit.probe.check.executed for passing target', async () => {
    const { server, url } = await startMockServer(200);
    try {
      const cfg: ProbeConfig = {
        interval: '10s',
        targets: [
          {
            name: 'mock',
            url,
            method: 'GET',
            timeout: '2s',
            expect: { status: 200 },
          },
        ],
      };

      const b = createBus();
      const events: Event[] = [];
      b.subscribe('kit.probe.#', (e) => events.push(e));

      const { Writable } = require('stream');
      const devNull = new Writable({ write: (_: any, __: any, cb: any) => cb() });
      const progress = new ProgressReporter(devNull, false);

      const results = await checkTargets(cfg, b, progress);
      b.close();

      expect(results).toHaveLength(1);
      expect(results[0].ok).toBe(true);
      expect(results[0].status).toBe(200);
      expect(events.length).toBeGreaterThanOrEqual(1);
      expect(events[0].topic).toBe('kit.probe.check.executed');
    } finally {
      server.close();
    }
  });

  it('emits kit.probe.check.alerted for failing target', async () => {
    const { server, url } = await startMockServer(500);
    try {
      const cfg: ProbeConfig = {
        interval: '10s',
        targets: [
          {
            name: 'bad',
            url,
            method: 'GET',
            timeout: '2s',
            expect: { status: 200 },
          },
        ],
      };

      const b = createBus();
      const topics: string[] = [];
      b.subscribe('kit.probe.#', (e) => topics.push(e.topic));

      const { Writable } = require('stream');
      const devNull = new Writable({ write: (_: any, __: any, cb: any) => cb() });
      const progress = new ProgressReporter(devNull, false);

      const results = await checkTargets(cfg, b, progress);
      b.close();

      expect(results).toHaveLength(1);
      expect(results[0].ok).toBe(false);
      expect(topics).toContain('kit.probe.check.executed');
      expect(topics).toContain('kit.probe.check.alerted');
    } finally {
      server.close();
    }
  });
});
