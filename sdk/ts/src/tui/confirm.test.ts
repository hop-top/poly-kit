/**
 * @module tui/confirm.test
 * Tests for the confirm() interactive prompt wrapper.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { buildTheme } from '../cli.js';

// Mock @clack/prompts before importing confirm.
vi.mock('@clack/prompts', () => ({
  confirm: vi.fn(),
  isCancel: vi.fn(),
}));

import * as clack from '@clack/prompts';
import { confirm } from './confirm.js';

const theme = buildTheme();

describe('confirm', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('returns true when clack confirm resolves true', async () => {
    vi.mocked(clack.confirm).mockResolvedValue(true);
    vi.mocked(clack.isCancel).mockReturnValue(false);

    const result = await confirm(theme, 'Continue?');
    expect(result).toBe(true);
  });

  it('returns false when clack confirm resolves false', async () => {
    vi.mocked(clack.confirm).mockResolvedValue(false);
    vi.mocked(clack.isCancel).mockReturnValue(false);

    const result = await confirm(theme, 'Continue?');
    expect(result).toBe(false);
  });

  it('throws Error("cancelled") when user cancels (Ctrl-C)', async () => {
    const cancelSymbol = Symbol('cancel');
    vi.mocked(clack.confirm).mockResolvedValue(cancelSymbol as unknown as boolean);
    vi.mocked(clack.isCancel).mockReturnValue(true);

    await expect(confirm(theme, 'Continue?')).rejects.toThrow('cancelled');
  });

  it('forwards message to clack confirm', async () => {
    vi.mocked(clack.confirm).mockResolvedValue(true);
    vi.mocked(clack.isCancel).mockReturnValue(false);

    await confirm(theme, 'Deploy to prod?');
    expect(clack.confirm).toHaveBeenCalledWith(
      expect.objectContaining({ message: 'Deploy to prod?' }),
    );
  });

  it('forwards initial option to clack confirm', async () => {
    vi.mocked(clack.confirm).mockResolvedValue(false);
    vi.mocked(clack.isCancel).mockReturnValue(false);

    await confirm(theme, 'Overwrite?', { initial: false });
    expect(clack.confirm).toHaveBeenCalledWith(
      expect.objectContaining({ initialValue: false }),
    );
  });
});
