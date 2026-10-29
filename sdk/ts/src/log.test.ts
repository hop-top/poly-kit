import { describe, it, expect, vi } from 'vitest';
import { createLogger, withVerbose } from './log';

function makeStderr(): { write: ReturnType<typeof vi.fn>; captured: () => string } {
  const chunks: string[] = [];
  const write = vi.fn((chunk: string) => { chunks.push(chunk); return true; });
  return { write, captured: () => chunks.join('') };
}

// Patch process.stderr.write for tests
function withStderr(fn: (captured: () => string) => void) {
  const orig = process.stderr.write;
  const s = makeStderr();
  process.stderr.write = s.write as unknown as typeof process.stderr.write;
  try {
    fn(s.captured);
  } finally {
    process.stderr.write = orig;
  }
}

// ---------------------------------------------------------------------------
// Format
// ---------------------------------------------------------------------------

describe('log format', () => {
  it('writes LEVEL msg to stderr', () => {
    withStderr((captured) => {
      const log = createLogger();
      log.info('hello');
      const out = captured();
      expect(out).toContain('INFO');
      expect(out).toContain('hello');
      expect(out).toMatch(/\n$/);
    });
  });

  it('formats key=val pairs', () => {
    withStderr((captured) => {
      const log = createLogger();
      log.info('start', 'port', 8080, 'host', 'localhost');
      const out = captured();
      expect(out).toContain('port=8080');
      expect(out).toContain('host=localhost');
    });
  });

  it('uses correct level prefixes', () => {
    withStderr((captured) => {
      const log = createLogger();
      log.error('e');
      log.warn('w');
      log.info('i');
      log.debug('d');
      const out = captured();
      expect(out).toContain('ERRO');
      expect(out).toContain('WARN');
      expect(out).toContain('INFO');
      expect(out).toContain('DEBU');
    });
  });
});

// ---------------------------------------------------------------------------
// Color
// ---------------------------------------------------------------------------

describe('log color', () => {
  it('includes ANSI codes by default', () => {
    withStderr((captured) => {
      const log = createLogger();
      log.error('fail');
      expect(captured()).toContain('\x1b[');
    });
  });

  it('strips ANSI when noColor=true', () => {
    withStderr((captured) => {
      const log = createLogger({ noColor: true });
      log.error('fail');
      expect(captured()).not.toContain('\x1b[');
    });
  });
});

// ---------------------------------------------------------------------------
// Quiet
// ---------------------------------------------------------------------------

describe('log quiet', () => {
  it('suppresses info when quiet=true', () => {
    withStderr((captured) => {
      const log = createLogger({ quiet: true });
      log.info('hidden');
      expect(captured()).toBe('');
    });
  });

  it('suppresses debug when quiet=true', () => {
    withStderr((captured) => {
      const log = createLogger({ quiet: true });
      log.debug('hidden');
      expect(captured()).toBe('');
    });
  });

  it('still shows warn when quiet=true', () => {
    withStderr((captured) => {
      const log = createLogger({ quiet: true });
      log.warn('visible');
      expect(captured()).toContain('WARN');
    });
  });

  it('still shows error when quiet=true', () => {
    withStderr((captured) => {
      const log = createLogger({ quiet: true });
      log.error('visible');
      expect(captured()).toContain('ERRO');
    });
  });
});

// ---------------------------------------------------------------------------
// Key-value edge cases
// ---------------------------------------------------------------------------

describe('log key-value edge cases', () => {
  it('quotes values with spaces', () => {
    withStderr((captured) => {
      const log = createLogger({ noColor: true });
      log.info('msg', 'path', '/my dir/file');
      expect(captured()).toContain('path="/my dir/file"');
    });
  });

  it('handles odd number of keyvals (trailing key)', () => {
    withStderr((captured) => {
      const log = createLogger({ noColor: true });
      log.info('msg', 'orphan');
      const out = captured();
      expect(out).toContain('orphan');
    });
  });
});

// ---------------------------------------------------------------------------
// Default output regression — no options must stay byte-identical
// ---------------------------------------------------------------------------

describe('log default output (regression pin)', () => {
  it('writes exact LEVEL msg k=v lines to process.stderr', () => {
    withStderr((captured) => {
      const log = createLogger({ noColor: true });
      log.info('start', 'port', 8080, 'path', '/my dir');
      log.warn('w');
      log.error('e', 'code', 2);
      log.debug('d');
      expect(captured()).toBe(
        'INFO start port=8080 path="/my dir"\n'
        + 'WARN w\n'
        + 'ERRO e code=2\n'
        + 'DEBU d\n',
      );
    });
  });

  it('keeps the themed ANSI prefixes when no option is passed', () => {
    withStderr((captured) => {
      const log = createLogger();
      log.info('i');
      log.error('e');
      expect(captured()).toBe(
        '\x1b[38;2;133;129;131mINFO\x1b[0m i\n'
        + '\x1b[1m\x1b[38;2;237;74;94mERRO\x1b[0m\x1b[22m e\n',
      );
    });
  });
});

// ---------------------------------------------------------------------------
// Injectable output stream
// ---------------------------------------------------------------------------

describe('log output option', () => {
  function sink(): { output: { write(chunk: string): boolean }; text: () => string } {
    const chunks: string[] = [];
    return {
      output: { write: (chunk: string) => { chunks.push(chunk); return true; } },
      text: () => chunks.join(''),
    };
  }

  it('createLogger writes to the injected output, not stderr', () => {
    withStderr((captured) => {
      const s = sink();
      const log = createLogger({ noColor: true, output: s.output });
      log.info('hello', 'k', 'v');
      expect(s.text()).toBe('INFO hello k=v\n');
      expect(captured()).toBe('');
    });
  });

  it('withVerbose writes to the injected output, not stderr', () => {
    withStderr((captured) => {
      const s = sink();
      const log = withVerbose({ noColor: true, verbose: 1, output: s.output });
      log.debug('dbg');
      expect(s.text()).toBe('DEBU dbg\n');
      expect(captured()).toBe('');
    });
  });

  it('injected output still honors quiet', () => {
    const s = sink();
    const log = createLogger({ quiet: true, noColor: true, output: s.output });
    log.info('hidden');
    log.warn('shown');
    expect(s.text()).toBe('WARN shown\n');
  });
});
