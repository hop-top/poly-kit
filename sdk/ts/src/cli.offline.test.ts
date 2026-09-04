/**
 * End-to-end guarantee for the `--offline` global, mirroring
 * `go/console/cli/offline_e2e_test.go`.
 *
 * A leaf that naively calls `fetch` — an adopter who never heard of the
 * offline marker — must still be refused when the user passes `--offline`.
 * Registering the flag alone would be advisory only; this test is what
 * makes the enforcement load-bearing.
 */

import { afterEach, describe, expect, it, vi } from 'vitest';

import { createCLI } from './cli';
import { isOffline, isOfflineError } from './netpolicy';

describe('--offline is enforced end to end', () => {
  const orig = globalThis.fetch;
  afterEach(() => {
    globalThis.fetch = orig;
  });

  it('refuses a naive leaf that uses globalThis.fetch', async () => {
    const reached = vi.fn(async () => new Response(null, { status: 204 }));
    globalThis.fetch = reached as unknown as typeof globalThis.fetch;

    let gotErr: unknown = null;
    const { program } = createCLI({ name: 'probe', version: '0.0.0', description: 'd' });
    program.command('fetch').action(async () => {
      try {
        // naive: no offline check whatsoever
        await fetch('https://example.invalid/x');
      } catch (e) {
        gotErr = e;
      }
    });

    await program.parseAsync(['node', 'probe', 'fetch', '--offline']);

    expect(isOfflineError(gotErr),
      `naive leaf reached the network under --offline: ${gotErr}`).toBe(true);
    expect(reached, 'the underlying fetch was invoked').not.toHaveBeenCalled();
  });

  it('leaves a leaf untouched without --offline', async () => {
    const reached = vi.fn(async () => new Response(null, { status: 204 }));
    globalThis.fetch = reached as unknown as typeof globalThis.fetch;

    let status = 0;
    const { program } = createCLI({ name: 'probe', version: '0.0.0', description: 'd' });
    program.command('fetch').action(async () => {
      status = (await fetch('https://example.invalid/x')).status;
    });

    await program.parseAsync(['node', 'probe', 'fetch']);

    expect(status).toBe(204);
    expect(reached).toHaveBeenCalledOnce();
  });

  it('exposes the marker to leaves that do consult it', async () => {
    let seen: boolean | null = null;
    const { program } = createCLI({ name: 'probe', version: '0.0.0', description: 'd' });
    program.command('check').action(() => {
      seen = isOffline();
    });

    await program.parseAsync(['node', 'probe', 'check', '--offline']);
    expect(seen).toBe(true);
  });

  it('registers --offline on the program', () => {
    const { program } = createCLI({ name: 'probe', version: '0.0.0', description: 'd' });
    expect(program.options.map((o) => o.flags)).toContain('--offline');
  });

  // --offline forces per-command network opt-outs ON, but must never
  // un-set an explicitly passed --no-* flag.
  it('forces opt-outs on without un-setting explicit --no-* flags', async () => {
    const { program } = createCLI({ name: 'probe', version: '0.0.0', description: 'd' });
    const sub = program.command('deploy')
      .option('--push', 'push after build', false)
      .option('--no-sync', 'skip sync');
    let opts: Record<string, unknown> = {};
    sub.action(() => { opts = sub.opts(); });

    await program.parseAsync(['node', 'probe', 'deploy', '--push', '--no-sync', '--offline']);

    // The explicit --no-sync stays off; --offline does not flip it back on.
    expect(opts['sync']).toBe(false);
    // The marker is what leaves consult to know the opt-in must not run.
    expect(program.opts()['offline']).toBe(true);
  });
});
