/**
 * @module tui
 * @package @hop-top/kit
 *
 * Barrel re-export of TUI display components.
 */

export { badge } from './badge.js';
export type { BadgeOpts } from './badge.js';

export { status } from './status.js';
export type { StatusKind } from './status.js';

export { pills } from './pills.js';

export { spinner } from './spinner.js';
export type { Spinner } from './spinner.js';

export { progress, clamp01, renderBarPlain } from './progress.js';
export type { Progress } from './progress.js';

export { anim, makeGradient } from './anim.js';
export type { Anim, AnimOpts } from './anim.js';

export { confirm } from './confirm.js';
export type { ConfirmOpts } from './confirm.js';

export { list } from './list.js';
export type { ListItem, ListOpts } from './list.js';
