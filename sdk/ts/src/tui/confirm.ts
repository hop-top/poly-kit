/**
 * @module tui/confirm
 * @package @hop-top/kit
 *
 * Interactive yes/no confirmation prompt.
 * Wraps @clack/prompts confirm(); throws on cancel (Ctrl-C).
 * Mirrors Go's NewConfirm from hop.top/kit/tui.
 */

import { confirm as clackConfirm, isCancel } from '@clack/prompts';
import type { Theme } from '../cli.js';

export interface ConfirmOpts {
  /** Default selection when user presses Enter without input. */
  initial?: boolean;
}

/**
 * Prompt the user with a yes/no question.
 *
 * @param _theme  - Active CLI theme (reserved for future accent styling).
 * @param message - Question to display.
 * @param opts    - Optional config; `initial` sets the default selection.
 * @returns       `true` if accepted, `false` if denied.
 * @throws        Error('cancelled') if user presses Ctrl-C.
 *
 * @example
 * ```ts
 * import { buildTheme } from '@hop-top/kit/cli';
 * import { confirm } from '@hop-top/kit/tui';
 * const ok = await confirm(buildTheme(), 'Deploy now?');
 * ```
 */
export async function confirm(
  _theme: Theme,
  message: string,
  opts?: ConfirmOpts,
): Promise<boolean> {
  const result = await clackConfirm({
    message,
    initialValue: opts?.initial,
  });

  if (isCancel(result)) {
    throw new Error('cancelled');
  }

  return result;
}
