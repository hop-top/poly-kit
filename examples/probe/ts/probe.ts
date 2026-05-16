/**
 * HTTP endpoint monitor using kit SDK packages (no CLI framework).
 * Demonstrates: config, bus, log, provenance, progress.
 */

import * as path from 'path';
import { load } from '../../../sdk/ts/src/config';
import { createBus } from '../../../sdk/ts/src/bus';
import { createLogger } from '../../../sdk/ts/src/log';
import { ProgressReporter } from '../../../sdk/ts/src/progress';
import { checkTargets, type ProbeConfig, type Result } from './core';

async function main(): Promise<void> {
  const cfg: ProbeConfig = {
    interval: '30s',
    targets: [],
  };

  const cfgPath = path.resolve(__dirname, '..', 'probe.yaml');
  load(cfg, { projectConfigPath: cfgPath });

  const b = createBus();
  const logger = createLogger();
  const progress = new ProgressReporter(
    process.stderr,
    process.stderr.isTTY ?? false,
  );

  // Subscribe to all probe events
  b.subscribe('probe.#', (event) => {
    const p = event.payload as Record<string, unknown>;
    logger.info('event',
      'topic', event.topic,
      'target', p.target,
      'ok', p.ok,
    );
  });

  const results = await checkTargets(cfg, b, progress);
  printSummary(results);
  b.close();
}

function printSummary(results: Result[]): void {
  console.log();
  console.log('=== Probe Summary ===');
  let passed = 0;
  let failed = 0;
  for (const r of results) {
    const status = r.ok ? 'PASS' : 'FAIL';
    if (r.ok) passed++;
    else failed++;
    const detail = r.error
      ? `error="${r.error}"`
      : `status=${r.status} latency=${r.latencyMs}ms`;
    console.log(
      `  [${status}] ${r.target.padEnd(12)} ${detail}`,
    );
  }
  console.log(
    `\nTotal: ${results.length}`
    + ` | Passed: ${passed}`
    + ` | Failed: ${failed}`,
  );
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
