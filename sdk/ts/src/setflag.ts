/**
 * @module setflag
 * @package @hop-top/kit
 *
 * SetFlag provides +append/-remove/=replace semantics for list-valued
 * CLI options. Use with Commander's custom parseArg to eliminate
 * --add-X/--remove-X/--clear-X flag proliferation.
 *
 * @example
 * ```ts
 * import { SetFlag } from '@hop-top/kit/setflag';
 *
 * const tags = new SetFlag();
 * program.option('--tag <val>', 'Manage tags', (v, prev) => tags.parseArg(v, prev));
 * // --tag feat --tag +docs --tag -bug --tag =a,b --tag =
 * ```
 */

/**
 * SetFlag manages a list of string values with +/-/= prefix operations.
 *
 *   val       append (default)
 *   +val      append (explicit)
 *   -val      remove
 *   =a,b      replace all
 *   =         clear all
 *
 * Comma-separated values are split. Duplicates suppressed.
 */
export class SetFlag {
  private items: string[];

  constructor(initial?: string[]) {
    this.items = initial ? [...initial] : [];
  }

  /** Apply a single +/-/= operation. */
  set(val: string): void {
    if (!val) return;

    switch (val[0]) {
      case '=': {
        const raw = val.slice(1);
        this.items = raw === '' ? [] : splitAndTrim(raw);
        return;
      }
      case '-': {
        const target = val.slice(1);
        this.items = this.items.filter(s => s !== target);
        return;
      }
      case '+':
        val = val.slice(1);
        break;
    }

    for (const v of splitAndTrim(val)) {
      if (!this.items.includes(v)) {
        this.items.push(v);
      }
    }
  }

  /** Add val literally (no prefix interpretation). */
  add(val: string): void {
    if (!this.items.includes(val)) this.items.push(val);
  }

  /** Remove val literally (no prefix interpretation). */
  remove(val: string): void {
    this.items = this.items.filter(s => s !== val);
  }

  /** Clear all items. */
  clear(): void {
    this.items = [];
  }

  /** Return a copy of current items. */
  values(): string[] {
    return [...this.items];
  }

  /** Comma-joined string representation. */
  toString(): string {
    return this.items.join(',');
  }

  /**
   * Commander-compatible parseArg callback.
   * Use as: `program.option('--tag <v>', 'desc', (v, prev) => sf.parseArg(v, prev))`
   */
  parseArg(value: string, previous: string[]): string[] {
    this.items = previous ? [...previous] : [];
    this.set(value);
    return this.values();
  }
}

function splitAndTrim(s: string): string[] {
  return s.split(',').map(p => p.trim()).filter(p => p !== '');
}
