import { describe, it, expect } from 'vitest';
import { buildTheme } from '../cli.js';
import { status } from './status.js';

const stripAnsi = (s: string) => s.replace(/\x1b\[[0-9;]*m/g, '');

describe('status()', () => {
  const theme = buildTheme();

  it('defaults to info kind', () => {
    const out = stripAnsi(status(theme, 'hello'));
    expect(out).toBe('ℹ hello');
  });

  it('info kind uses ℹ symbol', () => {
    const out = stripAnsi(status(theme, 'msg', 'info'));
    expect(out).toMatch(/^ℹ /);
  });

  it('success kind uses ✓ symbol', () => {
    const out = stripAnsi(status(theme, 'ok', 'success'));
    expect(out).toMatch(/^✓ /);
  });

  it('error kind uses ● symbol', () => {
    const out = stripAnsi(status(theme, 'fail', 'error'));
    expect(out).toMatch(/^● /);
  });

  it('warn kind uses ▲ symbol', () => {
    const out = stripAnsi(status(theme, 'careful', 'warn'));
    expect(out).toMatch(/^▲ /);
  });

  it('includes message text', () => {
    const out = stripAnsi(status(theme, 'operation complete', 'success'));
    expect(out).toContain('operation complete');
  });

  it('returns ANSI-colored output', () => {
    const out = status(theme, 'test', 'info');
    expect(out).toContain('\x1b[');
  });

  it('info uses theme.accent color', () => {
    // accent = #7ED957 → rgb(126,217,87)
    const out = status(theme, 'test', 'info');
    expect(out).toContain('38;2;126;217;87');
  });

  it('error uses theme.error color', () => {
    // error = #ED4A5E → rgb(237,74,94)
    const out = status(theme, 'test', 'error');
    expect(out).toContain('38;2;237;74;94');
  });

  it('success uses theme.success color', () => {
    // success = #52CF84 → rgb(82,207,132)
    const out = status(theme, 'test', 'success');
    expect(out).toContain('38;2;82;207;132');
  });

  it('warn uses theme.secondary color', () => {
    // secondary = #FF00FF → rgb(255,0,255)
    const out = status(theme, 'test', 'warn');
    expect(out).toContain('38;2;255;0;255');
  });
});
