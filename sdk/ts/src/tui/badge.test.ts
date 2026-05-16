import { describe, it, expect } from 'vitest';
import { buildTheme } from '../cli.js';
import { badge } from './badge.js';

const stripAnsi = (s: string) => s.replace(/\x1b\[[0-9;]*m/g, '');

describe('badge()', () => {
  const theme = buildTheme();

  it('renders text with surrounding spaces (padding)', () => {
    const out = stripAnsi(badge(theme, '^ UPDATE'));
    expect(out).toBe(' ^ UPDATE ');
  });

  it('returns non-empty ANSI string', () => {
    const out = badge(theme, 'v1.2.3');
    expect(out).toContain('\x1b[');
  });

  it('uses theme.accent as default background color', () => {
    const out = badge(theme, 'test');
    // accent is #7ED957 → rgb(126,217,87) → 48;2;126;217;87
    expect(out).toContain('48;2;126;217;87');
  });

  it('respects opts.color override', () => {
    const out = badge(theme, 'test', { color: '#FF0000' });
    // #FF0000 → rgb(255,0,0) → 48;2;255;0;0
    expect(out).toContain('48;2;255;0;0');
  });

  it('uses white foreground for contrast', () => {
    const out = badge(theme, 'test');
    expect(out).toContain('38;2;255;255;255');
  });

  it('ends with reset sequence', () => {
    const out = badge(theme, 'test');
    expect(out).toMatch(/\x1b\[0m$/);
  });
});
