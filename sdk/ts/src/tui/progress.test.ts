import { describe, it, expect, vi, afterEach } from 'vitest';
import { buildTheme } from '../cli.js';
import { progress, clamp01, renderBarPlain } from './progress.js';

const stripAnsi = (s: string) => s.replace(/\x1b\[[0-9;]*m/g, '');

function captureStderr(): { output: string[]; restore: () => void } {
  const output: string[] = [];
  vi.spyOn(process.stderr, 'write').mockImplementation((chunk: unknown) => {
    output.push(String(chunk));
    return true;
  });
  return { output, restore: () => vi.restoreAllMocks() };
}

describe('clamp01()', () => {
  it('clamps below 0 to 0', () => expect(clamp01(-1)).toBe(0));
  it('clamps above 1 to 1', () => expect(clamp01(2)).toBe(1));
  it('passes through 0', () => expect(clamp01(0)).toBe(0));
  it('passes through 1', () => expect(clamp01(1)).toBe(1));
  it('passes through mid value', () => expect(clamp01(0.5)).toBe(0.5));
});

describe('renderBarPlain()', () => {
  it('full bar at 1.0', () => {
    const bar = renderBarPlain(1.0);
    expect(bar).toBe('█'.repeat(30));
  });

  it('empty bar at 0.0', () => {
    const bar = renderBarPlain(0.0);
    expect(bar).toBe('░'.repeat(30));
  });

  it('half bar at 0.5', () => {
    const bar = renderBarPlain(0.5);
    expect(bar).toBe('█'.repeat(15) + '░'.repeat(15));
  });

  it('bar total width is always 30', () => {
    for (const v of [0, 0.1, 0.33, 0.5, 0.75, 1]) {
      const bar = renderBarPlain(v);
      // Count runes (all are single-char here)
      expect([...bar].length).toBe(30);
    }
  });
});

describe('progress()', () => {
  const theme = buildTheme();

  afterEach(() => vi.restoreAllMocks());

  it('start() renders bar to stderr', () => {
    const { output, restore } = captureStderr();
    const p = progress(theme);
    p.start('loading');
    expect(output.length).toBeGreaterThan(0);
    const plain = stripAnsi(output.join(''));
    expect(plain).toContain('loading');
    restore();
  });

  it('start() includes 0% indicator', () => {
    const { output, restore } = captureStderr();
    const p = progress(theme);
    p.start();
    const plain = stripAnsi(output.join(''));
    expect(plain).toContain('0%');
    restore();
  });

  it('update() renders updated percentage', () => {
    const { output, restore } = captureStderr();
    const p = progress(theme);
    p.start();
    output.length = 0;
    p.update(0.5);
    const plain = stripAnsi(output.join(''));
    expect(plain).toContain('50%');
    restore();
  });

  it('update() clamps negative value to 0', () => {
    const { output, restore } = captureStderr();
    const p = progress(theme);
    p.start();
    output.length = 0;
    p.update(-0.5);
    const plain = stripAnsi(output.join(''));
    expect(plain).toContain('0%');
    restore();
  });

  it('update() clamps value > 1 to 100%', () => {
    const { output, restore } = captureStderr();
    const p = progress(theme);
    p.start();
    output.length = 0;
    p.update(1.5);
    const plain = stripAnsi(output.join(''));
    expect(plain).toContain('100%');
    restore();
  });

  it('update() includes optional message', () => {
    const { output, restore } = captureStderr();
    const p = progress(theme);
    p.start();
    output.length = 0;
    p.update(0.3, 'fetching');
    const plain = stripAnsi(output.join(''));
    expect(plain).toContain('fetching');
    restore();
  });

  it('stop() clears line', () => {
    const { output, restore } = captureStderr();
    const p = progress(theme);
    p.start();
    output.length = 0;
    p.stop();
    expect(output.join('')).toContain('\r');
    restore();
  });

  it('stop() prints final message', () => {
    const { output, restore } = captureStderr();
    const p = progress(theme);
    p.start();
    output.length = 0;
    p.stop('finished');
    expect(output.join('')).toContain('finished');
    restore();
  });

  it('renders accent color for filled chars', () => {
    const { output, restore } = captureStderr();
    const p = progress(theme);
    p.start();
    p.update(1.0);
    // accent #7ED957 → 38;2;126;217;87
    expect(output.join('')).toContain('38;2;126;217;87');
    restore();
  });

  it('renders muted color for empty chars', () => {
    const { output, restore } = captureStderr();
    const p = progress(theme);
    p.start();
    p.update(0.0);
    // muted #858183 → 38;2;133;129;131
    expect(output.join('')).toContain('38;2;133;129;131');
    restore();
  });

  it('accepts optional total parameter (ignored for ratio)', () => {
    // Should not throw when total is provided.
    const p = progress(theme, 100);
    const { restore } = captureStderr();
    expect(() => { p.start(); p.update(0.5); p.stop(); }).not.toThrow();
    restore();
  });
});
