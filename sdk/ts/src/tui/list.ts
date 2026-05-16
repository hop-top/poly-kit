/**
 * @module tui/list
 * @package @hop-top/kit
 *
 * Interactive selection list prompt.
 * Wraps @clack/prompts select(); throws on cancel (Ctrl-C).
 * Mirrors Go's NewList from hop.top/kit/tui (selection variant).
 */

import { select, isCancel } from '@clack/prompts';
import type { Theme } from '../cli.js';

export interface ListItem {
  /** Display label shown in the prompt. */
  label: string;
  /** Value returned when this item is selected. */
  value: string;
  /** Optional hint shown alongside the label. */
  hint?: string;
}

export interface ListOpts {
  /** Value of the initially highlighted item. */
  initial?: string;
}

/**
 * Prompt the user to pick one item from a list.
 *
 * @param _theme  - Active CLI theme (reserved for future accent styling).
 * @param message - Prompt label shown above the list.
 * @param items   - Selectable options.
 * @param opts    - Optional config; `initial` sets the pre-selected value.
 * @returns       The `value` of the selected item.
 * @throws        Error('cancelled') if user presses Ctrl-C.
 *
 * @example
 * ```ts
 * import { buildTheme } from '@hop-top/kit/cli';
 * import { list } from '@hop-top/kit/tui';
 * const env = await list(buildTheme(), 'Pick environment', [
 *   { label: 'Production', value: 'prod' },
 *   { label: 'Staging',    value: 'staging', hint: 'default' },
 * ]);
 * ```
 */
export async function list(
  _theme: Theme,
  message: string,
  items: ListItem[],
  opts?: ListOpts,
): Promise<string> {
  const options = items.map((item) => ({
    label: item.label,
    value: item.value,
    hint: item.hint,
  }));

  const initialValue = opts?.initial;

  const result = await select({
    message,
    options,
    initialValue,
  });

  if (isCancel(result)) {
    throw new Error('cancelled');
  }

  return result as string;
}
