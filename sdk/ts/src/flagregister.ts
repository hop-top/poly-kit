/**
 * @module flagregister
 * @package @hop-top/kit
 *
 * Registers SetFlag/TextFlag on a Commander program with configurable
 * display style (prefix-only, verbose-only, or both).
 */

import { Command } from 'commander';
import { SetFlag } from './setflag';
import { TextFlag } from './textflag';

export enum FlagDisplay {
  Prefix = 'prefix',
  Verbose = 'verbose',
  Both = 'both',
}

/**
 * Register a set-valued flag with +/-/= prefix support.
 * Returns the shared SetFlag instance.
 */
export function registerSetFlag(
  cmd: Command, name: string, usage: string,
  display: FlagDisplay = FlagDisplay.Prefix,
): SetFlag {
  const sf = new SetFlag();

  const showPrefix = display === FlagDisplay.Prefix || display === FlagDisplay.Both;
  const showVerbose = display === FlagDisplay.Verbose || display === FlagDisplay.Both;

  if (showPrefix) {
    cmd.option(`--${name} <val>`, usage + ' (+add, -remove, =replace)',
      (v: string) => { sf.set(v); return sf.values(); });
  }

  if (showVerbose) {
    cmd.option(`--add-${name} <val>`, `Add to ${usage}`,
      (v: string) => { sf.add(v); return sf.values(); });
    cmd.option(`--remove-${name} <val>`, `Remove from ${usage}`,
      (v: string) => { sf.remove(v); return sf.values(); });
    cmd.option(`--clear-${name}`, `Clear all ${usage}`,
      () => { sf.clear(); return sf.values(); });
  }

  return sf;
}

/**
 * Register a text-valued flag with +/^/= prefix support.
 * Returns the shared TextFlag instance.
 */
export function registerTextFlag(
  cmd: Command, name: string, usage: string,
  display: FlagDisplay = FlagDisplay.Prefix,
): TextFlag {
  const tf = new TextFlag();

  cmd.option(`--${name} <val>`, usage,
    (v: string, prev: string) => tf.parseArg(v, prev));

  const showVerbose = display === FlagDisplay.Verbose || display === FlagDisplay.Both;

  if (showVerbose) {
    cmd.option(`--${name}-append <val>`, `Append to ${usage} (new line)`,
      (v: string) => { tf.append(v); return tf.value(); });
    cmd.option(`--${name}-append-inline <val>`, `Append to ${usage} (inline)`,
      (v: string) => { tf.appendInline(v); return tf.value(); });
    cmd.option(`--${name}-prepend <val>`, `Prepend to ${usage} (new line)`,
      (v: string) => { tf.prepend(v); return tf.value(); });
    cmd.option(`--${name}-prepend-inline <val>`, `Prepend to ${usage} (inline)`,
      (v: string) => { tf.prependInline(v); return tf.value(); });
  }

  return tf;
}

export { SetFlag, TextFlag };
