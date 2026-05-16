import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { buildTheme } from '../cli.js';
import { spinner } from './spinner.js';

// Capture stderr writes.
function captureStderr(): { output: string[]; restore: () => void } {
  const output: string[] = [];
  vi.spyOn(process.stderr, 'write').mockImplementation((chunk: unknown) => {
    output.push(String(chunk));
    return true;
  });
  return { output, restore: () => vi.restoreAllMocks() };
}

describe('spinner()', () => {
  const theme = buildTheme();

  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('start() writes initial frame to stderr', () => {
    const { output, restore } = captureStderr();
    const s = spinner(theme);
    s.start('loading');
    expect(output.length).toBeGreaterThan(0);
    expect(output[0]).toContain('loading');
    restore();
  });

  it('start() includes theme.accent ANSI color', () => {
    const { output, restore } = captureStderr();
    const s = spinner(theme);
    s.start('x');
    // accent #7ED957 → 38;2;126;217;87
    expect(output[0]).toContain('38;2;126;217;87');
    restore();
  });

  it('advances frames on interval tick', () => {
    const { output, restore } = captureStderr();
    const s = spinner(theme);
    s.start('tick');
    const countBefore = output.length;
    vi.advanceTimersByTime(80 * 3);
    expect(output.length).toBeGreaterThan(countBefore);
    s.stop();
    restore();
  });

  it('stop() clears line', () => {
    const { output, restore } = captureStderr();
    const s = spinner(theme);
    s.start();
    output.length = 0; // reset
    s.stop();
    const joined = output.join('');
    expect(joined).toContain('\r');
    restore();
  });

  it('stop() prints final message', () => {
    const { output, restore } = captureStderr();
    const s = spinner(theme);
    s.start();
    output.length = 0;
    s.stop('done!');
    const joined = output.join('');
    expect(joined).toContain('done!');
    restore();
  });

  it('stop() without message does not print extra newline content', () => {
    const { output, restore } = captureStderr();
    const s = spinner(theme);
    s.start();
    output.length = 0;
    s.stop();
    // Should only clear line, no message string
    expect(output.join('')).not.toContain('done');
    restore();
  });

  it('message() updates displayed text on next tick', () => {
    const { output, restore } = captureStderr();
    const s = spinner(theme);
    s.start('first');
    s.message('updated');
    vi.advanceTimersByTime(80);
    const joined = output.join('');
    expect(joined).toContain('updated');
    s.stop();
    restore();
  });

  it('stop() stops interval — no further renders after stop', () => {
    const { output, restore } = captureStderr();
    const s = spinner(theme);
    s.start();
    s.stop();
    const countAfterStop = output.length;
    vi.advanceTimersByTime(500);
    expect(output.length).toBe(countAfterStop);
    restore();
  });
});
