import { describe, it, expect } from 'vitest';
import {
  parseOptions,
  type OptionSpec,
} from '../../src/output/formatter';

const stringSpec: OptionSpec = { name: 'delim', type: 'string', usage: 'sep' };
const intSpec: OptionSpec = { name: 'indent', type: 'int', usage: 'spaces' };
const boolSpec: OptionSpec = { name: 'no-header', type: 'bool', usage: 'omit' };
const enumSpec: OptionSpec = {
  name: 'style',
  type: 'enum',
  enum: ['kv', 'lines', 'paragraph'],
  usage: 's',
};

describe('parseOptions — string', () => {
  it('keeps value as-is', () => {
    const opts = parseOptions(['delim=,'], [stringSpec]);
    expect(opts['delim']).toBe(',');
  });

  it('returns empty string when value side is empty', () => {
    const opts = parseOptions(['delim='], [stringSpec]);
    expect(opts['delim']).toBe('');
  });
});

describe('parseOptions — int', () => {
  it('coerces digits', () => {
    const opts = parseOptions(['indent=4'], [intSpec]);
    expect(opts['indent']).toBe(4);
  });

  it('accepts negative ints', () => {
    const opts = parseOptions(['indent=-1'], [intSpec]);
    expect(opts['indent']).toBe(-1);
  });

  it('throws on non-int', () => {
    expect(() => parseOptions(['indent=abc'], [intSpec])).toThrow(/not an int/);
  });

  it('throws on float', () => {
    expect(() => parseOptions(['indent=2.5'], [intSpec])).toThrow(/not an int/);
  });
});

describe('parseOptions — bool', () => {
  it('coerces "true"', () => {
    const opts = parseOptions(['no-header=true'], [boolSpec]);
    expect(opts['no-header']).toBe(true);
  });

  it('coerces "false"', () => {
    const opts = parseOptions(['no-header=false'], [boolSpec]);
    expect(opts['no-header']).toBe(false);
  });

  it('coerces "1" / "0"', () => {
    expect(parseOptions(['no-header=1'], [boolSpec])['no-header']).toBe(true);
    expect(parseOptions(['no-header=0'], [boolSpec])['no-header']).toBe(false);
  });

  it('key-only form for bool spec defaults to true', () => {
    const opts = parseOptions(['no-header'], [boolSpec]);
    expect(opts['no-header']).toBe(true);
  });

  it('throws on key-only form for non-bool spec', () => {
    expect(() => parseOptions(['delim'], [stringSpec])).toThrow(/requires a value/);
  });

  it('throws on garbage bool value', () => {
    expect(() => parseOptions(['no-header=yeah'], [boolSpec])).toThrow(/not a bool/);
  });
});

describe('parseOptions — enum', () => {
  it('accepts allowed value', () => {
    const opts = parseOptions(['style=lines'], [enumSpec]);
    expect(opts['style']).toBe('lines');
  });

  it('throws on out-of-enum', () => {
    expect(() => parseOptions(['style=tree'], [enumSpec])).toThrow(
      /not in \{kv, lines, paragraph\}/,
    );
  });
});

describe('parseOptions — unknown key', () => {
  it('throws with valid-keys list', () => {
    expect(() =>
      parseOptions(['bogus=x'], [stringSpec, intSpec]),
    ).toThrow(/unknown option "bogus" \(valid: delim, indent\)/);
  });

  it('throws on empty key', () => {
    expect(() => parseOptions(['=x'], [stringSpec])).toThrow(/empty option key/);
  });
});

describe('parseOptions — defaults', () => {
  it('fills missing keys from spec defaults', () => {
    const specs: OptionSpec[] = [
      { name: 'delim', type: 'string', default: ',', usage: '' },
      { name: 'crlf', type: 'bool', default: false, usage: '' },
    ];
    const opts = parseOptions([], specs);
    expect(opts['delim']).toBe(',');
    expect(opts['crlf']).toBe(false);
  });

  it('user value overrides default', () => {
    const specs: OptionSpec[] = [
      { name: 'delim', type: 'string', default: ',', usage: '' },
    ];
    const opts = parseOptions(['delim=;'], specs);
    expect(opts['delim']).toBe(';');
  });

  it('omits undefined defaults', () => {
    const specs: OptionSpec[] = [
      { name: 'delim', type: 'string', usage: '' },
    ];
    const opts = parseOptions([], specs);
    expect('delim' in opts).toBe(false);
  });
});

describe('parseOptions — value with embedded =', () => {
  it('splits on first = only', () => {
    const opts = parseOptions(['delim=a=b=c'], [stringSpec]);
    expect(opts['delim']).toBe('a=b=c');
  });
});
