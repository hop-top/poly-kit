/**
 * Load-bearing coverage for the verbosity/streams wiring in `cli.ts`.
 *
 * These feed a *constructed* ParityData (never the shared parity.json) whose
 * values differ from the shipped contract, and assert the flag names, label
 * and destination follow it. A port that re-hardcodes `-V`, `--stream`,
 * `[name] ` or `process.stderr` fails here.
 *
 * Mirrors `go/console/cli/verbose_parity_wiring_test.go` and
 * `go/console/cli/streams_parity_wiring_test.go`.
 */

import { Command } from 'commander';
import { describe, expect, it } from 'vitest';

import {
  channel,
  createCLI,
  registerStream,
  streamFlagName,
  streamLabel,
  streamOutput,
  verbosityFlagSpec,
  verbosityShorthand,
} from './cli';
import { parity, type ParityData } from './tui/parity';

/** A ParityData carrying only the given streams block. */
function streamsData(flag: string, labelFormat: string, output: string): ParityData {
  return { streams: { flag, label_format: labelFormat, output } } as unknown as ParityData;
}

/** A ParityData carrying only the given verbosity flag. */
function verbosityData(flag: string): ParityData {
  return { verbosity: { flag, levels: {}, quiet_override: 'warn' } } as unknown as ParityData;
}

/**
 * Name a resolved destination. `process.stdout` and `process.stderr` render
 * identically in vitest diffs, so assert on the name instead — a failure
 * then reads "expected 'stderr' to be 'stdout'".
 */
function destName(w: NodeJS.WritableStream): string {
  if (w === process.stdout) return 'stdout';
  if (w === process.stderr) return 'stderr';
  return 'other';
}

/** Capture what a body writes to stdout and stderr. */
function captureStreams(fn: () => void): { out: string; err: string } {
  const outChunks: string[] = [];
  const errChunks: string[] = [];
  const oo = process.stdout.write;
  const oe = process.stderr.write;
  process.stdout.write = ((c: string) => { outChunks.push(c); return true; }) as never;
  process.stderr.write = ((c: string) => { errChunks.push(c); return true; }) as never;
  try {
    fn();
  } finally {
    process.stdout.write = oo;
    process.stderr.write = oe;
  }
  return { out: outChunks.join(''), err: errChunks.join('') };
}

// ─── Load-bearing: streams ───────────────────────────────────────────────────

/**
 * Swaps the label template, destination and flag name away from the shipped
 * `[{name}]` / stderr / `--stream`. Hardcoded literals fail here.
 */
describe('streams follow contract not literals', () => {
  const d = streamsData('--channel', '<<{name}>>', 'stdout');

  it('renders the contract label_format', () => {
    expect(streamLabel(d, 'trace'), 'label must render label_format, not a hardcoded [name]')
      .toBe('<<trace>> ');
  });

  it('resolves the contract output destination', () => {
    expect(destName(streamOutput(d)),
      'destination must follow streams.output, not a hardcoded stderr').toBe('stdout');
  });

  it('resolves the contract flag name', () => {
    expect(streamFlagName(d), 'flag name must follow streams.flag, not a hardcoded "stream"')
      .toBe('channel');
  });
});

// ─── Load-bearing: verbosity flag ────────────────────────────────────────────

/**
 * Swaps the shorthand away from the shipped `-V`. A hardcoded `'-V, --verbose'`
 * option spec fails here.
 */
describe('verbosity flag follows contract not literals', () => {
  it('takes the shorthand from the contract', () => {
    expect(verbosityShorthand(verbosityData('-d')),
      'shorthand must follow verbosity.flag, not a hardcoded V').toBe('d');
    expect(verbosityFlagSpec(verbosityData('-d')),
      'option spec must be built from the contract shorthand').toBe('-d, --verbose');
  });

  it('registers a diverging shorthand on a real commander program', () => {
    // Commander takes the option spec as a runtime string, so the shorthand
    // need not be a compile-time literal. This proves the wiring would work
    // if the contract changed.
    const p = new Command('t');
    p.option(verbosityFlagSpec(verbosityData('-d')), 'Increase verbosity',
      (_: string, prev: number) => prev + 1, 0);
    p.command('run').action(() => {});
    p.parse(['node', 't', 'run', '-dd']);
    expect(p.opts().verbose).toBe(2);
  });
});

// ─── Requirement pins: observable behavior unchanged ─────────────────────────

/**
 * Pins the wiring to the values actually declared in parity.json. NOT
 * load-bearing: it keeps passing if a literal is restored, by design.
 */
describe('streams match shipped contract', () => {
  it('pins the contract values', () => {
    expect(parity.streams.label_format).toBe('[{name}]');
    expect(parity.streams.output).toBe('stderr');
    expect(parity.streams.flag).toBe('--stream');
  });

  it('renders the historical [name] prefix to stderr', () => {
    expect(streamLabel(parity, 'trace')).toBe('[trace] ');
    expect(destName(streamOutput(parity))).toBe('stderr');
    expect(streamFlagName(parity)).toBe('stream');
  });

  it('registers --stream and prefixes enabled channels end-to-end', () => {
    const { program } = createCLI({ name: 't', version: '1.0.0', description: 'd' });
    const sub = program.command('run');
    registerStream(sub, 'sql', 'SQL queries');
    expect(sub.options.map((o) => o.flags)).toContain('--stream <names>');

    program.parse(['node', 't', 'run', '--stream', 'sql']);
    const { out, err } = captureStreams(() => {
      channel(sub, 'sql').write('SELECT 1\n');
      channel(sub, 'off').write('hidden\n');
    });
    expect(err).toBe('[sql] SELECT 1\n');
    expect(out).toBe('');
  });
});

describe('verbosity flag matches shipped contract', () => {
  it('pins the contract flag', () => {
    expect(parity.verbosity.flag).toBe('-V');
    expect(verbosityShorthand(parity)).toBe('V');
    expect(verbosityFlagSpec(parity)).toBe('-V, --verbose');
  });

  it('registers -V as a stacking count on the real CLI', () => {
    const { program } = createCLI({ name: 't', version: '1.0.0', description: 'd' });
    program.command('run').action(() => {});
    program.parse(['node', 't', 'run', '-VVV']);
    expect(program.opts().verbose).toBe(3);
  });
});

// ─── Degradation ─────────────────────────────────────────────────────────────

describe('streams degrade safely', () => {
  it('falls back on an empty contract', () => {
    const empty = streamsData('', '', '');
    expect(streamLabel(empty, 'trace'), 'absent label_format falls back to [name]')
      .toBe('[trace] ');
    expect(destName(streamOutput(empty)), 'absent output falls back to stderr').toBe('stderr');
    expect(streamFlagName(empty), 'absent flag falls back to "stream"').toBe('stream');
  });

  it('renders a label_format without {name} verbatim', () => {
    const noPlaceholder = streamsData('--s', 'LOG:', 'stderr');
    expect(streamLabel(noPlaceholder, 'trace')).toBe('LOG: ');
    expect(streamFlagName(noPlaceholder)).toBe('s');
  });
});

describe('verbosity flag degrades safely', () => {
  it('falls back when the contract flag is absent or not a shorthand', () => {
    expect(verbosityShorthand(verbosityData('')), 'absent flag falls back to V').toBe('V');
    expect(verbosityShorthand(verbosityData('--verbose')),
      'a multi-character flag is not a commander shorthand; fall back to V').toBe('V');
  });
});
