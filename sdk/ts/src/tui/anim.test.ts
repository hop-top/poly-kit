import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { buildTheme } from '../cli.js';
import { anim, makeGradient } from './anim.js';

function captureStderr(): { output: string[]; restore: () => void } {
  const output: string[] = [];
  vi.spyOn(process.stderr, 'write').mockImplementation((chunk: unknown) => {
    output.push(String(chunk));
    return true;
  });
  return { output, restore: () => vi.restoreAllMocks() };
}

describe('makeGradient()', () => {
  it('returns empty array for 0 steps', () => {
    expect(makeGradient('#000000', '#FFFFFF', 0)).toEqual([]);
  });

  it('returns single color for 1 step (start color)', () => {
    const g = makeGradient('#FF0000', '#0000FF', 1);
    expect(g).toHaveLength(1);
    expect(g[0]).toEqual([255, 0, 0]);
  });

  it('returns correct start and end colors for 2 steps', () => {
    const g = makeGradient('#FF0000', '#0000FF', 2);
    expect(g).toHaveLength(2);
    expect(g[0]).toEqual([255, 0, 0]);
    expect(g[1]).toEqual([0, 0, 255]);
  });

  it('returns length matching steps', () => {
    const g = makeGradient('#000000', '#FFFFFF', 10);
    expect(g).toHaveLength(10);
  });

  it('interpolates midpoint approximately', () => {
    const g = makeGradient('#000000', '#FFFFFF', 3);
    // midpoint should be ~128
    expect(g[1][0]).toBeGreaterThan(120);
    expect(g[1][0]).toBeLessThan(136);
  });
});

describe('anim()', () => {
  const theme = buildTheme();

  beforeEach(() => vi.useFakeTimers());
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('start() writes initial frame to stderr', () => {
    const { output, restore } = captureStderr();
    const a = anim(theme);
    a.start();
    expect(output.length).toBeGreaterThan(0);
    expect(output[0]).toContain('\r');
    a.stop();
    restore();
  });

  it('start() includes ANSI color codes', () => {
    const { output, restore } = captureStderr();
    const a = anim(theme);
    a.start();
    expect(output[0]).toContain('\x1b[38;2;');
    a.stop();
    restore();
  });

  it('renders on each interval tick', () => {
    const { output, restore } = captureStderr();
    const a = anim(theme);
    a.start();
    const before = output.length;
    vi.advanceTimersByTime(100 * 3);
    expect(output.length).toBeGreaterThan(before);
    a.stop();
    restore();
  });

  it('stop() clears line', () => {
    const { output, restore } = captureStderr();
    const a = anim(theme);
    a.start();
    output.length = 0;
    a.stop();
    expect(output.join('')).toContain('\r');
    restore();
  });

  it('stop() halts interval — no renders after stop', () => {
    const { output, restore } = captureStderr();
    const a = anim(theme);
    a.start();
    a.stop();
    const countAfter = output.length;
    vi.advanceTimersByTime(500);
    expect(output.length).toBe(countAfter);
    restore();
  });

  it('setLabel() updates label in subsequent renders', () => {
    const { output, restore } = captureStderr();
    const a = anim(theme, { label: 'init' });
    a.start();
    a.setLabel('updated');
    vi.advanceTimersByTime(100);
    const joined = output.join('');
    expect(joined).toContain('updated');
    a.stop();
    restore();
  });

  it('respects custom width option', () => {
    const { output, restore } = captureStderr();
    const a = anim(theme, { width: 5 });
    a.start();
    // 5 chars → 5 reset sequences in one frame
    const resets = (output[0].match(/\x1b\[0m/g) ?? []).length;
    expect(resets).toBe(5);
    a.stop();
    restore();
  });

  it('initial label appears in first render', () => {
    const { output, restore } = captureStderr();
    const a = anim(theme, { label: 'hello' });
    a.start();
    expect(output[0]).toContain('hello');
    a.stop();
    restore();
  });

  it('uses gradient colors from accent to secondary', () => {
    const { output, restore } = captureStderr();
    const a = anim(theme, { width: 1 });
    a.start();
    // accent #7ED957 → 38;2;126;217;87 (first/only gradient step for width=1)
    expect(output[0]).toContain('38;2;126;217;87');
    a.stop();
    restore();
  });
});
