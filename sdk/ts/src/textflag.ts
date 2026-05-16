/**
 * @module textflag
 * @package @hop-top/kit
 *
 * TextFlag provides +append/^prepend/=replace semantics for text-valued
 * CLI options.
 *
 * @example
 * ```ts
 * import { TextFlag } from '@hop-top/kit/textflag';
 *
 * const desc = new TextFlag();
 * program.option('--desc <val>', 'Description', (v, prev) => desc.parseArg(v, prev));
 * ```
 */

export class TextFlag {
  private text: string;

  constructor(initial?: string) {
    this.text = initial ?? '';
  }

  set(val: string): void {
    if (!val) return;

    if (val.startsWith('+=')) {
      this.text += val.slice(2);
    } else if (val[0] === '+') {
      const body = val.slice(1);
      this.text = this.text === '' ? body : this.text + '\n' + body;
    } else if (val.startsWith('^=')) {
      this.text = val.slice(2) + this.text;
    } else if (val[0] === '^') {
      const body = val.slice(1);
      this.text = this.text === '' ? body : body + '\n' + this.text;
    } else if (val[0] === '=') {
      this.text = val.slice(1);
    } else {
      this.text = val;
    }
  }

  /** Append val on new line (no prefix interpretation). */
  append(val: string): void {
    this.text = this.text === '' ? val : this.text + '\n' + val;
  }

  /** Append val inline (no prefix interpretation). */
  appendInline(val: string): void { this.text += val; }

  /** Prepend val on new line (no prefix interpretation). */
  prepend(val: string): void {
    this.text = this.text === '' ? val : val + '\n' + this.text;
  }

  /** Prepend val inline (no prefix interpretation). */
  prependInline(val: string): void { this.text = val + this.text; }

  value(): string { return this.text; }
  toString(): string { return this.text; }

  parseArg(value: string, previous: string): string {
    this.text = previous ?? '';
    this.set(value);
    return this.value();
  }
}
