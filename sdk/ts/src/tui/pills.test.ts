import { describe, it, expect } from 'vitest';
import { buildTheme } from '../cli.js';
import { pills } from './pills.js';

const stripAnsi = (s: string) => s.replace(/\x1b\[[0-9;]*m/g, '');

describe('pills()', () => {
  const theme = buildTheme();

  it('returns empty string for empty array', () => {
    expect(pills(theme, [])).toBe('');
  });

  it('renders single item with surrounding spaces', () => {
    const out = stripAnsi(pills(theme, ['main']));
    expect(out).toBe(' main ');
  });

  it('joins multiple items with a space separator', () => {
    const out = stripAnsi(pills(theme, ['branch: main', 'env: prod']));
    expect(out).toBe(' branch: main   env: prod ');
  });

  it('returns ANSI-colored output', () => {
    const out = pills(theme, ['test']);
    expect(out).toContain('\x1b[');
  });

  it('uses theme.secondary color', () => {
    // secondary = #FF00FF → rgb(255,0,255)
    const out = pills(theme, ['item']);
    expect(out).toContain('38;2;255;0;255');
  });

  it('each pill ends with reset sequence', () => {
    const out = pills(theme, ['a', 'b']);
    const resets = (out.match(/\x1b\[0m/g) ?? []).length;
    expect(resets).toBe(2);
  });
});
