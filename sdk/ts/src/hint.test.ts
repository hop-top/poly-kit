import { describe, it, expect, afterEach } from 'vitest';
import { Writable } from 'node:stream';
import {
  HintSet, active, hintsEnabled, renderHints,
  registerUpgradeHints, registerVersionHints,
} from './hint.js';

// ---------------------------------------------------------------------------
// HintSet
// ---------------------------------------------------------------------------

describe('HintSet', () => {
  it('lookup returns empty array when nothing registered', () => {
    const s = new HintSet();
    expect(s.lookup('foo')).toEqual([]);
  });

  it('register + lookup round-trips a single hint', () => {
    const s = new HintSet();
    s.register('foo', { message: 'do something' });
    expect(s.lookup('foo')).toHaveLength(1);
    expect(s.lookup('foo')[0].message).toBe('do something');
  });

  it('register accumulates multiple hints for same command', () => {
    const s = new HintSet();
    s.register('foo', { message: 'a' }, { message: 'b' });
    s.register('foo', { message: 'c' });
    expect(s.lookup('foo')).toHaveLength(3);
  });

  it('lookup returns independent copy — mutation does not affect set', () => {
    const s = new HintSet();
    s.register('foo', { message: 'x' });
    const copy = s.lookup('foo');
    copy.push({ message: 'injected' });
    expect(s.lookup('foo')).toHaveLength(1);
  });

  it('hints for different commands are independent', () => {
    const s = new HintSet();
    s.register('a', { message: 'hint-a' });
    s.register('b', { message: 'hint-b' });
    expect(s.lookup('a')[0].message).toBe('hint-a');
    expect(s.lookup('b')[0].message).toBe('hint-b');
  });
});

// ---------------------------------------------------------------------------
// active
// ---------------------------------------------------------------------------

describe('active', () => {
  it('returns all hints when conditions are undefined', () => {
    const hints = [{ message: 'a' }, { message: 'b' }];
    expect(active(hints)).toHaveLength(2);
  });

  it('filters out hints whose condition returns false', () => {
    const hints = [
      { message: 'yes', condition: () => true },
      { message: 'no',  condition: () => false },
    ];
    const result = active(hints);
    expect(result).toHaveLength(1);
    expect(result[0].message).toBe('yes');
  });

  it('returns empty array for empty input', () => {
    expect(active([])).toEqual([]);
  });
});

// ---------------------------------------------------------------------------
// hintsEnabled
// ---------------------------------------------------------------------------

describe('hintsEnabled', () => {
  afterEach(() => {
    delete process.env['HOP_QUIET_HINTS'];
  });

  it('returns true by default', () => {
    expect(hintsEnabled({})).toBe(true);
  });

  it('returns false when noHints=true', () => {
    expect(hintsEnabled({ noHints: true })).toBe(false);
  });

  it('returns false when quiet=true', () => {
    expect(hintsEnabled({ quiet: true })).toBe(false);
  });

  it('returns false when hintsEnabled=false', () => {
    expect(hintsEnabled({ hintsEnabled: false })).toBe(false);
  });

  it('returns false when HOP_QUIET_HINTS=1', () => {
    process.env['HOP_QUIET_HINTS'] = '1';
    expect(hintsEnabled({})).toBe(false);
  });

  it('returns false when HOP_QUIET_HINTS=true', () => {
    process.env['HOP_QUIET_HINTS'] = 'true';
    expect(hintsEnabled({})).toBe(false);
  });

  it('returns false when HOP_QUIET_HINTS=yes', () => {
    process.env['HOP_QUIET_HINTS'] = 'yes';
    expect(hintsEnabled({})).toBe(false);
  });

  it('returns true when HOP_QUIET_HINTS=0', () => {
    process.env['HOP_QUIET_HINTS'] = '0';
    expect(hintsEnabled({})).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// renderHints
// ---------------------------------------------------------------------------

/** Writable that captures written bytes as a string. */
class StringWriter extends Writable {
  chunks: string[] = [];
  _write(chunk: Buffer | string, _enc: string, done: () => void) {
    this.chunks.push(chunk.toString());
    done();
  }
  get value(): string { return this.chunks.join(''); }
}

/** A Writable flagged as TTY so renderHints doesn't skip it. */
class TTYWriter extends StringWriter {
  readonly isTTY = true;
}

describe('renderHints', () => {
  afterEach(() => {
    delete process.env['HOP_QUIET_HINTS'];
    delete process.env['NO_COLOR'];
  });

  it('no-op when format is json', () => {
    const w = new TTYWriter();
    renderHints(w, [{ message: 'x' }], 'json', {}, '#858183');
    expect(w.value).toBe('');
  });

  it('no-op when format is yaml', () => {
    const w = new TTYWriter();
    renderHints(w, [{ message: 'x' }], 'yaml', {}, '#858183');
    expect(w.value).toBe('');
  });

  it('no-op when noHints=true', () => {
    const w = new TTYWriter();
    renderHints(w, [{ message: 'x' }], 'table', { noHints: true }, '#858183');
    expect(w.value).toBe('');
  });

  it('no-op when quiet=true', () => {
    const w = new TTYWriter();
    renderHints(w, [{ message: 'x' }], 'table', { quiet: true }, '#858183');
    expect(w.value).toBe('');
  });

  it('no-op when hints list is empty', () => {
    const w = new TTYWriter();
    renderHints(w, [], 'table', {}, '#858183');
    expect(w.value).toBe('');
  });

  it('no-op when stream is not a TTY', () => {
    const w = new StringWriter(); // isTTY not set
    renderHints(w, [{ message: 'x' }], 'table', {}, '#858183');
    expect(w.value).toBe('');
  });

  it('renders hint with → prefix to TTY', () => {
    process.env['NO_COLOR'] = '1';
    const w = new TTYWriter();
    renderHints(w, [{ message: 'do this next' }], 'table', {}, '#858183');
    expect(w.value).toContain('→ do this next');
  });

  it('renders a leading blank line before hints', () => {
    process.env['NO_COLOR'] = '1';
    const w = new TTYWriter();
    renderHints(w, [{ message: 'x' }], 'table', {}, '#858183');
    expect(w.value.startsWith('\n')).toBe(true);
  });

  it('renders multiple active hints', () => {
    process.env['NO_COLOR'] = '1';
    const w = new TTYWriter();
    renderHints(w, [{ message: 'a' }, { message: 'b' }], 'table', {}, '#858183');
    expect(w.value).toContain('→ a');
    expect(w.value).toContain('→ b');
  });

  it('skips hints whose condition returns false', () => {
    process.env['NO_COLOR'] = '1';
    const w = new TTYWriter();
    renderHints(w, [
      { message: 'yes', condition: () => true },
      { message: 'no',  condition: () => false },
    ], 'table', {}, '#858183');
    expect(w.value).toContain('→ yes');
    expect(w.value).not.toContain('→ no');
  });

  it('includes ANSI color codes when NO_COLOR not set', () => {
    const w = new TTYWriter();
    renderHints(w, [{ message: 'colored' }], 'table', {}, '#858183');
    expect(w.value).toContain('\x1b[');
  });

  it('strips ANSI codes when NO_COLOR env set', () => {
    process.env['NO_COLOR'] = '1';
    const w = new TTYWriter();
    renderHints(w, [{ message: 'plain' }], 'table', {}, '#858183');
    expect(w.value).not.toContain('\x1b[');
  });
});

// ---------------------------------------------------------------------------
// Standard factories
// ---------------------------------------------------------------------------

describe('registerUpgradeHints', () => {
  it('registers hint for "upgrade" command', () => {
    const s = new HintSet();
    const upgraded = false;
    registerUpgradeHints(s, 'mytool', () => upgraded);
    expect(s.lookup('upgrade')).toHaveLength(1);
  });

  it('hint is inactive when upgraded=false', () => {
    const s = new HintSet();
    const upgraded = false;
    registerUpgradeHints(s, 'mytool', () => upgraded);
    expect(active(s.lookup('upgrade'))).toHaveLength(0);
  });

  it('hint is active when upgraded=true', () => {
    const s = new HintSet();
    let upgraded = false;
    registerUpgradeHints(s, 'mytool', () => upgraded);
    upgraded = true;
    const result = active(s.lookup('upgrade'));
    expect(result).toHaveLength(1);
    expect(result[0].message).toContain('mytool version');
  });
});

describe('registerVersionHints', () => {
  it('registers hint for "version" command', () => {
    const s = new HintSet();
    const avail = false;
    registerVersionHints(s, 'mytool', () => avail);
    expect(s.lookup('version')).toHaveLength(1);
  });

  it('hint is inactive when updateAvail=false', () => {
    const s = new HintSet();
    const avail = false;
    registerVersionHints(s, 'mytool', () => avail);
    expect(active(s.lookup('version'))).toHaveLength(0);
  });

  it('hint is active when updateAvail=true', () => {
    const s = new HintSet();
    let avail = false;
    registerVersionHints(s, 'mytool', () => avail);
    avail = true;
    const result = active(s.lookup('version'));
    expect(result).toHaveLength(1);
    expect(result[0].message).toContain('mytool upgrade');
  });
});

// ---------------------------------------------------------------------------
// createCLI integration — --no-hints flag
// ---------------------------------------------------------------------------

describe('createCLI --no-hints integration', () => {
  it('--no-hints flag registered by default', async () => {
    const { createCLI } = await import('./cli.js');
    const { program } = createCLI({ name: 'mytool', version: '1.0.0', description: 'A tool' });
    const opt = program.options.find(o => o.long === '--no-hints');
    expect(opt).toBeDefined();
  });

  it('--no-hints absent when disable.hints=true', async () => {
    const { createCLI } = await import('./cli.js');
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
      disable: { hints: true },
    });
    const opt = program.options.find(o => o.long === '--no-hints');
    expect(opt).toBeUndefined();
  });

  it('--no-hints survives when disable.format=true (decoupled)', async () => {
    const { createCLI } = await import('./cli.js');
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
      disable: { format: true },
    });
    const opt = program.options.find(o => o.long === '--no-hints');
    expect(opt).toBeDefined();
  });
});
