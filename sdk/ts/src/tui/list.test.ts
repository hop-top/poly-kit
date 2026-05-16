/**
 * @module tui/list.test
 * Tests for the list() interactive prompt wrapper.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { buildTheme } from '../cli.js';

// Mock @clack/prompts before importing list.
vi.mock('@clack/prompts', () => ({
  select: vi.fn(),
  isCancel: vi.fn(),
}));

import * as clack from '@clack/prompts';
import { list } from './list.js';
import type { ListItem } from './list.js';

const theme = buildTheme();

const items: ListItem[] = [
  { label: 'Production', value: 'prod' },
  { label: 'Staging', value: 'staging', hint: 'safer' },
  { label: 'Dev', value: 'dev' },
];

describe('list', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('returns the selected value', async () => {
    vi.mocked(clack.select).mockResolvedValue('staging');
    vi.mocked(clack.isCancel).mockReturnValue(false);

    const result = await list(theme, 'Pick environment', items);
    expect(result).toBe('staging');
  });

  it('throws Error("cancelled") when user cancels (Ctrl-C)', async () => {
    const cancelSymbol = Symbol('cancel');
    vi.mocked(clack.select).mockResolvedValue(cancelSymbol as unknown as string);
    vi.mocked(clack.isCancel).mockReturnValue(true);

    await expect(list(theme, 'Pick environment', items)).rejects.toThrow('cancelled');
  });

  it('passes items correctly to clack select', async () => {
    vi.mocked(clack.select).mockResolvedValue('prod');
    vi.mocked(clack.isCancel).mockReturnValue(false);

    await list(theme, 'Pick environment', items);

    expect(clack.select).toHaveBeenCalledWith(
      expect.objectContaining({
        options: [
          { label: 'Production', value: 'prod', hint: undefined },
          { label: 'Staging', value: 'staging', hint: 'safer' },
          { label: 'Dev', value: 'dev', hint: undefined },
        ],
      }),
    );
  });

  it('forwards message to clack select', async () => {
    vi.mocked(clack.select).mockResolvedValue('dev');
    vi.mocked(clack.isCancel).mockReturnValue(false);

    await list(theme, 'Choose target', items);
    expect(clack.select).toHaveBeenCalledWith(
      expect.objectContaining({ message: 'Choose target' }),
    );
  });

  it('forwards initial option to clack select', async () => {
    vi.mocked(clack.select).mockResolvedValue('staging');
    vi.mocked(clack.isCancel).mockReturnValue(false);

    await list(theme, 'Pick environment', items, { initial: 'staging' });
    expect(clack.select).toHaveBeenCalledWith(
      expect.objectContaining({ initialValue: 'staging' }),
    );
  });
});
