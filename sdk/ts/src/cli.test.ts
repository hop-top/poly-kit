import { describe, it, expect, afterEach } from 'vitest';
import { createCLI, buildTheme, applyHelpTheme, Neon, Dark,
         setCommandGroup, verboseCount, registerStream,
         channel, getStreamDefs } from './cli';

describe('createCLI', () => {
  it('returns { program, theme }', () => {
    const result = createCLI({ name: 'mytool', version: '1.2.3', description: 'A tool' });
    expect(result).toHaveProperty('program');
    expect(result).toHaveProperty('theme');
  });

  it('exposes -v/--version as option, not a subcommand', () => {
    const { program } = createCLI({ name: 'mytool', version: '1.2.3', description: 'A tool' });
    const subNames = program.commands.map(c => c.name());
    expect(subNames).not.toContain('version');
    expect(subNames).not.toContain('help');
    expect(subNames).not.toContain('completion');
  });

  it('has --format, --quiet, --no-color global options', () => {
    const { program } = createCLI({ name: 'mytool', version: '1.2.3', description: 'A tool' });
    const optNames = program.options.map(o => o.long);
    expect(optNames).toContain('--format');
    expect(optNames).toContain('--quiet');
    expect(optNames).toContain('--no-color');
  });

  it('--format defaults to table', () => {
    const { program } = createCLI({ name: 'mytool', version: '1.2.3', description: 'A tool' });
    program.parse(['node', 'mytool']);
    expect(program.opts().format).toBe('table');
  });
});

describe('Palette constants', () => {
  it('Neon has expected hex values', () => {
    expect(Neon.command).toBe('#7ED957');
    expect(Neon.flag).toBe('#FF00FF');
  });

  it('Dark has expected hex values', () => {
    expect(Dark.command).toBe('#C1FF72');
    expect(Dark.flag).toBe('#FF66C4');
  });
});

// ANSI SGR sequences expected per color.
const ANSI_WHITE   = '\x1b[1;38;2;255;255;255m'; // #FFFFFF bold (section headers)
const ANSI_FLAG    = '\x1b[38;2;255;0;255m';      // #FF00FF (Neon.Flag)
const ANSI_CMD     = '\x1b[38;2;126;217;87m';     // #7ED957 (Neon.Command)
const ANSI_ARG     = '\x1b[38;2;181;232;155m';    // #B5E89B (Argument)
const ANSI_RESET   = '\x1b[0m';

describe('applyHelpTheme', () => {
  afterEach(() => {
    // Clean up any NO_COLOR env override.
    delete process.env['NO_COLOR'];
  });

  it('help output contains ANSI color codes when color enabled', () => {
    const { program } = createCLI({ name: 'mytool', version: '1.2.3', description: 'A tool' });
    program.command('run').description('Run something');
    const help = program.helpInformation();
    expect(help).toContain('\x1b[');
  });

  it('section header (FLAGS:) is white bold', () => {
    const { program } = createCLI({ name: 'mytool', version: '1.2.3', description: 'A tool' });
    const help = program.helpInformation();
    expect(help).toContain(ANSI_WHITE + 'FLAGS:' + ANSI_RESET);
  });

  it('flag names are colored Neon.Flag (#FF00FF)', () => {
    const { program } = createCLI({ name: 'mytool', version: '1.2.3', description: 'A tool' });
    const help = program.helpInformation();
    // --help flag should appear colored.
    expect(help).toContain(ANSI_FLAG + '--help' + ANSI_RESET);
  });

  it('command names are colored Neon.Command (#7ED957)', () => {
    const { program } = createCLI({ name: 'mytool', version: '1.2.3', description: 'A tool' });
    program.command('run').description('Run something');
    const help = program.helpInformation();
    // The "COMMANDS:" section header appears colored (parity.json renames "Commands:" → "COMMANDS:").
    expect(help).toContain(ANSI_WHITE + 'COMMANDS:' + ANSI_RESET);
  });

  it('no ANSI codes when --no-color in argv', () => {
    const origArgv = process.argv;
    try {
      process.argv = ['node', 'mytool', '--no-color'];
      const { program } = createCLI({ name: 'mytool', version: '1.2.3', description: 'A tool' });
      const help = program.helpInformation();
      expect(help).not.toContain('\x1b[');
    } finally {
      process.argv = origArgv;
    }
  });

  it('no ANSI codes when NO_COLOR env var set', () => {
    process.env['NO_COLOR'] = '1';
    const { program } = createCLI({ name: 'mytool', version: '1.2.3', description: 'A tool' });
    const help = program.helpInformation();
    expect(help).not.toContain('\x1b[');
  });

  it('applyHelpTheme with noColor=true suppresses all ANSI', () => {
    const { program, theme } = createCLI({ name: 'mytool', version: '1.2.3', description: 'A tool' });
    // Re-apply with noColor=true to override.
    applyHelpTheme(program, theme, true);
    const help = program.helpInformation();
    expect(help).not.toContain('\x1b[');
  });

  it('argument words in angle brackets are colored #B5E89B', () => {
    const { program } = createCLI({ name: 'mytool', version: '1.2.3', description: 'A tool' });
    program.command('build <target>').description('Build a target');
    const help = program.helpInformation();
    expect(help).toContain(ANSI_ARG);
  });

  it('hyphenated words in descriptions are NOT flag-colored', () => {
    // --quiet already registered by createCLI; no need to add again.
    const { program } = createCLI({ name: 'mytool', version: '1.2.3', description: 'A tool' });
    const help = program.helpInformation();
    expect(help).not.toContain(ANSI_FLAG + '-essential' + ANSI_RESET);
    expect(help).toContain(ANSI_FLAG + '--quiet' + ANSI_RESET);
  });

  it('ALL-CAPS words in descriptions are NOT arg-colored', () => {
    // --no-color already registered by createCLI; no need to add again.
    const { program } = createCLI({ name: 'mytool', version: '1.2.3', description: 'A tool' });
    const help = program.helpInformation();
    expect(help).not.toContain(ANSI_ARG + 'ANSI' + ANSI_RESET);
  });

  it('command names in COMMANDS section are colored accent', () => {
    const { program } = createCLI({ name: 'mytool', version: '1.2.3', description: 'A tool' });
    program.command('deploy').description('Deploy the app');
    const help = program.helpInformation();
    expect(help).toContain(ANSI_CMD + 'deploy' + ANSI_RESET);
  });

  it('descriptions are colored muted', () => {
    const { program, theme } = createCLI({ name: 'mytool', version: '1.2.3', description: 'A tool' });
    program.command('deploy').description('Deploy the app');
    const help = program.helpInformation();
    const ansiMuted = `\x1b[38;2;${parseInt(theme.muted.slice(1,3),16)};${parseInt(theme.muted.slice(3,5),16)};${parseInt(theme.muted.slice(5,7),16)}m`;
    expect(help).toContain(ansiMuted);
  });
});

// ---------------------------------------------------------------------------
// Help structure — line-by-line parity
// ---------------------------------------------------------------------------

/** Strip ANSI SGR sequences from a string. */
function stripANSI(s: string): string {
  return s.replace(/\x1b\[[^m]*m/g, '');
}

/** Return plain (ANSI-stripped) help lines for a program with one subcommand. */
function helpLines(): string[] {
  const { program } = createCLI({ name: 'mytool', version: '1.2.3', description: 'A tool' });
  program.command('sub').description('A subcommand');
  return stripANSI(program.helpInformation()).split('\n');
}

describe('help structure — line-by-line parity', () => {
  it('first non-empty line is the Usage line', () => {
    const lines = helpLines();
    const first = lines.find(l => l.trim().length > 0) ?? '';
    expect(first).toMatch(/^Usage:/);
  });

  it('description appears before any section header', () => {
    const lines = helpLines();
    const descIdx  = lines.findIndex(l => l.includes('A tool'));
    const secIdx   = lines.findIndex(l => /^(COMMANDS|FLAGS|Options|Commands):/.test(l.trim()));
    expect(descIdx).toBeGreaterThanOrEqual(0);
    expect(secIdx).toBeGreaterThanOrEqual(0);
    expect(descIdx).toBeLessThan(secIdx);
  });

  it('COMMANDS: section appears before FLAGS: section', () => {
    const lines = helpLines();
    const cmdIdx  = lines.findIndex(l => l.trim() === 'COMMANDS:');
    const flagIdx = lines.findIndex(l => l.trim() === 'FLAGS:');
    expect(cmdIdx).toBeGreaterThanOrEqual(0);
    expect(flagIdx).toBeGreaterThanOrEqual(0);
    expect(cmdIdx).toBeLessThan(flagIdx);
  });

  it('sub command appears under COMMANDS: section', () => {
    const lines = helpLines();
    const cmdIdx = lines.findIndex(l => l.trim() === 'COMMANDS:');
    const subIdx = lines.findIndex(l => /^\s+sub\b/.test(l));
    expect(subIdx).toBeGreaterThan(cmdIdx);
  });

  it('--format appears under FLAGS: section', () => {
    const lines = helpLines();
    const flagIdx  = lines.findIndex(l => l.trim() === 'FLAGS:');
    const fmtIdx   = lines.findIndex(l => l.includes('--format'));
    expect(fmtIdx).toBeGreaterThan(flagIdx);
  });

  it('--quiet appears under FLAGS: section', () => {
    const lines = helpLines();
    const flagIdx   = lines.findIndex(l => l.trim() === 'FLAGS:');
    const quietIdx  = lines.findIndex(l => l.includes('--quiet'));
    expect(quietIdx).toBeGreaterThan(flagIdx);
  });

  it('--no-color appears under FLAGS: section', () => {
    const lines = helpLines();
    const flagIdx    = lines.findIndex(l => l.trim() === 'FLAGS:');
    const ncIdx      = lines.findIndex(l => l.includes('--no-color'));
    expect(ncIdx).toBeGreaterThan(flagIdx);
  });

  it('no "help" or "completion" appears as a command entry', () => {
    const lines = helpLines();
    const cmdIdx = lines.findIndex(l => l.trim() === 'COMMANDS:');
    const flagIdx = lines.findIndex(l => l.trim() === 'FLAGS:');
    const cmdLines = lines.slice(cmdIdx + 1, flagIdx);
    expect(cmdLines.some(l => /^\s+help\b/.test(l))).toBe(false);
    expect(cmdLines.some(l => /^\s+completion\b/.test(l))).toBe(false);
  });

  it('section headers are COMMANDS and FLAGS (not Commands and Options)', () => {
    const lines = helpLines();
    expect(lines.some(l => l.trim() === 'Commands:')).toBe(false);
    expect(lines.some(l => l.trim() === 'Options:')).toBe(false);
    expect(lines.some(l => l.trim() === 'COMMANDS:')).toBe(true);
    expect(lines.some(l => l.trim() === 'FLAGS:')).toBe(true);
  });

  it('flags section contains --format, --quiet, --no-color, --no-hints', () => {
    const lines = helpLines();
    const flagIdx = lines.findIndex(l => l.trim() === 'FLAGS:');
    const flagLines = lines.slice(flagIdx + 1).filter(l => l.trim().length > 0);
    for (const flag of ['--format', '--quiet', '--no-color', '--no-hints']) {
      expect(flagLines.some(l => l.includes(flag))).toBe(true);
    }
  });
});

describe('CLIConfig disable', () => {
  it('disable.format=true → no format key in opts after parse', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
      disable: { format: true },
    });
    program.parse(['node', 'mytool']);
    expect(program.opts()).not.toHaveProperty('format');
  });

  it('disable.quiet=true → no quiet key in opts after parse', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
      disable: { quiet: true },
    });
    program.parse(['node', 'mytool']);
    expect(program.opts()).not.toHaveProperty('quiet');
  });

  it('disable.noColor=true → no color key in opts after parse', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
      disable: { noColor: true },
    });
    program.parse(['node', 'mytool']);
    // commander uses camelCase 'color' for --no-color
    expect(program.opts()).not.toHaveProperty('noColor');
  });

  it('disable.format=true does NOT suppress --no-hints', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
      disable: { format: true },
    });
    program.parse(['node', 'mytool']);
    expect(program.opts()).not.toHaveProperty('format');
    expect(program.opts()).toHaveProperty('hints');
  });
});

describe('CLIConfig globals', () => {
  it('single global flag appears in opts with correct default', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
      globals: [{ name: 'endpoint', usage: 'API endpoint URL', default: 'http://localhost' }],
    });
    program.parse(['node', 'mytool']);
    expect(program.opts().endpoint).toBe('http://localhost');
  });

  it('global flag with short → shorthand registered', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
      globals: [{ name: 'debug', short: 'D', usage: 'Debug output', default: 'off' }],
    });
    program.parse(['node', 'mytool', '-D', 'on']);
    expect(program.opts().debug).toBe('on');
  });

  it('global flag with no default → empty string', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
      globals: [{ name: 'token', usage: 'Auth token' }],
    });
    program.parse(['node', 'mytool']);
    expect(program.opts().token).toBe('');
  });
});

describe('help.showAliases', () => {
  it('aliases hidden by default', () => {
    const { program } = createCLI({ name: 'mytool', version: '1.0.0', description: 'A tool' });
    program.command('deploy').alias('d').description('Deploy the app');
    const help = stripANSI(program.helpInformation());
    expect(help).toContain('deploy');
    expect(help).not.toContain('deploy|d');
    expect(help).not.toContain('(aliases:');
  });

  it('aliases shown when showAliases=true', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
      help: { showAliases: true },
    });
    program.command('deploy').alias('d').aliases(['dp']).description('Deploy the app');
    const help = stripANSI(program.helpInformation());
    expect(help).toContain('deploy');
    expect(help).toContain('d, dp');
  });
});

// ---------------------------------------------------------------------------
// Command groups — COMMANDS vs MANAGEMENT with --help-all
// ---------------------------------------------------------------------------

/** Strip ANSI SGR sequences from a string (duplicated for self-contained block). */
function stripAnsi(s: string): string {
  return s.replace(/\x1b\[[^m]*m/g, '');
}

describe('command groups', () => {
  it('management commands hidden by default', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
      groups: [{ id: 'management', title: 'MANAGEMENT', hidden: true }],
    });
    program.command('run').description('Run something');
    const mgmt = program.command('config').description('Manage config');
    setCommandGroup(mgmt, 'management');

    const help = stripAnsi(program.helpInformation());
    expect(help).toContain('COMMANDS:');
    expect(help).toContain('run');
    expect(help).not.toContain('MANAGEMENT:');
    expect(help).not.toContain('config');
  });

  it('management commands shown with --help-all', () => {
    const origArgv = process.argv;
    try {
      process.argv = ['node', 'mytool', '--help-all'];
      const { program } = createCLI({
        name: 'mytool', version: '1.0.0', description: 'A tool',
        groups: [{ id: 'management', title: 'MANAGEMENT', hidden: true }],
      });
      program.command('run').description('Run something');
      const mgmt = program.command('config').description('Manage config');
      setCommandGroup(mgmt, 'management');

      const help = stripAnsi(program.helpInformation());
      expect(help).toContain('COMMANDS:');
      expect(help).toContain('run');
      expect(help).toContain('MANAGEMENT:');
      expect(help).toContain('config');
    } finally {
      process.argv = origArgv;
    }
  });

  it('custom group renders with title', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
      groups: [{ id: 'advanced', title: 'ADVANCED COMMANDS' }],
    });
    program.command('run').description('Run something');
    const adv = program.command('debug').description('Debug internals');
    setCommandGroup(adv, 'advanced');

    const help = stripAnsi(program.helpInformation());
    expect(help).toContain('COMMANDS:');
    expect(help).toContain('run');
    expect(help).toContain('ADVANCED COMMANDS:');
    expect(help).toContain('debug');
  });

  it('ungrouped commands stay in default COMMANDS section', () => {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
      groups: [{ id: 'management', title: 'MANAGEMENT', hidden: true }],
    });
    program.command('run').description('Run something');
    program.command('build').description('Build something');
    const mgmt = program.command('config').description('Manage config');
    setCommandGroup(mgmt, 'management');

    const help = stripAnsi(program.helpInformation());
    const lines = help.split('\n');
    const cmdIdx = lines.findIndex(l => l.trim() === 'COMMANDS:');
    const flagIdx = lines.findIndex(l => l.trim() === 'FLAGS:');
    const cmdSection = lines.slice(cmdIdx + 1, flagIdx).join('\n');
    expect(cmdSection).toContain('run');
    expect(cmdSection).toContain('build');
    expect(cmdSection).not.toContain('config');
  });
});

// ---------------------------------------------------------------------------
// Per-group help — --help-<id> + help <id> subcommand
// ---------------------------------------------------------------------------

describe('per-group help', () => {
  /** Helper: create a CLI with management + extras groups and commands. */
  function makeGroupedCLI() {
    const { program } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool',
      groups: [
        { id: 'management', title: 'MANAGEMENT', hidden: true },
        { id: 'extras', title: 'EXTRAS' },
      ],
    });
    program.command('run').description('Run something');
    program.command('build').description('Build something');
    const cfg = program.command('config').description('Manage config');
    setCommandGroup(cfg, 'management');
    const diag = program.command('diagnostics').description('Run diagnostics');
    setCommandGroup(diag, 'management');
    const bonus = program.command('bonus').description('Bonus feature');
    setCommandGroup(bonus, 'extras');
    return program;
  }

  it('per-group --help-management shows only management commands', () => {
    const origArgv = process.argv;
    try {
      process.argv = ['node', 'mytool', '--help-management'];
      const program = makeGroupedCLI();
      const help = stripAnsi(program.helpInformation());
      expect(help).toContain('MANAGEMENT:');
      expect(help).toContain('config');
      expect(help).toContain('diagnostics');
      expect(help).toContain('FLAGS:');
      // Should NOT contain other groups or default commands section
      expect(help).not.toContain('COMMANDS:');
      expect(help).not.toContain('run');
      expect(help).not.toContain('build');
      expect(help).not.toContain('EXTRAS:');
      expect(help).not.toContain('bonus');
    } finally {
      process.argv = origArgv;
    }
  });

  it('help management subcommand shows only management commands', () => {
    const origArgv = process.argv;
    try {
      process.argv = ['node', 'mytool', '--help-management'];
      const program = makeGroupedCLI();
      const help = stripAnsi(program.helpInformation());
      // Same assertions as --help-management
      expect(help).toContain('MANAGEMENT:');
      expect(help).toContain('config');
      expect(help).not.toContain('COMMANDS:');
      expect(help).not.toContain('run');
    } finally {
      process.argv = origArgv;
    }
  });

  it('help all shows everything', () => {
    const origArgv = process.argv;
    try {
      process.argv = ['node', 'mytool', '--help-all'];
      const program = makeGroupedCLI();
      const help = stripAnsi(program.helpInformation());
      expect(help).toContain('COMMANDS:');
      expect(help).toContain('run');
      expect(help).toContain('build');
      expect(help).toContain('MANAGEMENT:');
      expect(help).toContain('config');
      expect(help).toContain('EXTRAS:');
      expect(help).toContain('bonus');
    } finally {
      process.argv = origArgv;
    }
  });

  it('--help-extras shows only extras group', () => {
    const origArgv = process.argv;
    try {
      process.argv = ['node', 'mytool', '--help-extras'];
      const program = makeGroupedCLI();
      const help = stripAnsi(program.helpInformation());
      expect(help).toContain('EXTRAS:');
      expect(help).toContain('bonus');
      expect(help).toContain('FLAGS:');
      expect(help).not.toContain('COMMANDS:');
      expect(help).not.toContain('MANAGEMENT:');
    } finally {
      process.argv = origArgv;
    }
  });
});

describe('buildTheme', () => {
  it('defaults to Neon palette when no accent', () => {
    const theme = buildTheme();
    expect(theme.palette.command).toBe(Neon.command);
    expect(theme.palette.flag).toBe(Neon.flag);
    expect(theme.accent).toBe(Neon.command);
    expect(theme.secondary).toBe(Neon.flag);
  });

  it('overrides palette.command when accent provided', () => {
    const theme = buildTheme('#FF0000');
    expect(theme.palette.command).toBe('#FF0000');
    expect(theme.accent).toBe('#FF0000');
    // flag unchanged
    expect(theme.palette.flag).toBe(Neon.flag);
  });

  it('has correct semantic colors', () => {
    const theme = buildTheme();
    expect(theme.muted).toBe('#858183');
    expect(theme.error).toBe('#ED4A5E');
    expect(theme.success).toBe('#52CF84');
  });

  it('createCLI wires accent into theme', () => {
    const { theme } = createCLI({
      name: 'mytool', version: '1.0.0', description: 'A tool', accent: '#AABBCC',
    });
    expect(theme.accent).toBe('#AABBCC');
    expect(theme.palette.command).toBe('#AABBCC');
  });

  it('createCLI without accent uses Neon default', () => {
    const { theme } = createCLI({ name: 'mytool', version: '1.0.0', description: 'A tool' });
    expect(theme.accent).toBe(Neon.command);
  });
});

// ---------------------------------------------------------------------------
// Verbose flag (-V)
// ---------------------------------------------------------------------------

describe('verbose flag', () => {
  it('default verbose count is 0', () => {
    const { program } = createCLI({ name: 't', version: '0.1.0', description: 't' });
    program.parse(['node', 't']);
    expect(program.opts().verbose).toBe(0);
  });

  it('single -V yields count 1', () => {
    const { program } = createCLI({ name: 't', version: '0.1.0', description: 't' });
    program.parse(['node', 't', '-V']);
    expect(program.opts().verbose).toBe(1);
  });

  it('stacked -VV yields count 2', () => {
    const { program } = createCLI({ name: 't', version: '0.1.0', description: 't' });
    program.parse(['node', 't', '-VV']);
    expect(program.opts().verbose).toBe(2);
  });

  it('--verbose --verbose stacks to 2', () => {
    const { program } = createCLI({ name: 't', version: '0.1.0', description: 't' });
    program.parse(['node', 't', '--verbose', '--verbose']);
    expect(program.opts().verbose).toBe(2);
  });

  it('verboseCount accessor reads from preAction hook', async () => {
    const { program } = createCLI({ name: 't', version: '0.1.0', description: 't' });
    let count = -1;
    program.command('run').action(function (this: any) {
      count = verboseCount(this);
    });
    await program.parseAsync(['node', 't', '-VV', 'run']);
    expect(count).toBe(2);
  });

  it('quiet overrides verbose in preAction', async () => {
    const { program } = createCLI({ name: 't', version: '0.1.0', description: 't' });
    let count = -1;
    program.command('run').action(function (this: any) {
      count = verboseCount(this);
    });
    await program.parseAsync(['node', 't', '-VV', '--quiet', 'run']);
    expect(count).toBe(0);
  });
});

// ---------------------------------------------------------------------------
// Named streams
// ---------------------------------------------------------------------------

describe('named streams', () => {
  it('registerStream adds --stream option', () => {
    const { program } = createCLI({ name: 't', version: '0.1.0', description: 't' });
    const sub = program.command('run');
    registerStream(sub, 'sql', 'SQL queries');
    const optNames = sub.options.map(o => o.long);
    expect(optNames).toContain('--stream');
  });

  it('channel returns noop when stream not enabled', () => {
    const { program } = createCLI({ name: 't', version: '0.1.0', description: 't' });
    const sub = program.command('run').action(() => {});
    registerStream(sub, 'sql', 'SQL queries');
    program.parse(['node', 't', 'run']);
    const ch = channel(sub, 'sql');
    // noop writer returns true but writes nothing
    expect(ch.write('test')).toBe(true);
  });

  it('channel writes prefixed output when stream enabled', () => {
    const { program } = createCLI({ name: 't', version: '0.1.0', description: 't' });
    const sub = program.command('run').action(() => {});
    registerStream(sub, 'sql', 'SQL queries');
    program.parse(['node', 't', 'run', '--stream', 'sql']);

    const chunks: string[] = [];
    const origWrite = process.stderr.write;
    process.stderr.write = ((s: any) => {
      chunks.push(String(s)); return true;
    }) as any;
    try {
      channel(sub, 'sql').write('SELECT 1\n');
    } finally {
      process.stderr.write = origWrite;
    }
    expect(chunks[0]).toBe('[sql] SELECT 1\n');
  });

  it('comma-separated --stream enables multiple streams', () => {
    const { program } = createCLI({ name: 't', version: '0.1.0', description: 't' });
    const sub = program.command('run').action(() => {});
    registerStream(sub, 'sql', 'SQL queries');
    registerStream(sub, 'http', 'HTTP requests');
    program.parse(['node', 't', 'run', '--stream', 'sql,http']);

    const chunks: string[] = [];
    const origWrite = process.stderr.write;
    process.stderr.write = ((s: any) => {
      chunks.push(String(s)); return true;
    }) as any;
    try {
      channel(sub, 'sql').write('q\n');
      channel(sub, 'http').write('r\n');
    } finally {
      process.stderr.write = origWrite;
    }
    expect(chunks).toEqual(['[sql] q\n', '[http] r\n']);
  });

  it('getStreamDefs returns registered streams', () => {
    const { program } = createCLI({ name: 't', version: '0.1.0', description: 't' });
    const sub = program.command('run');
    registerStream(sub, 'sql', 'SQL queries');
    registerStream(sub, 'http', 'HTTP requests');
    const defs = getStreamDefs(sub);
    expect(defs).toHaveLength(2);
    expect(defs[0]).toEqual({ name: 'sql', description: 'SQL queries' });
    expect(defs[1]).toEqual({ name: 'http', description: 'HTTP requests' });
  });

  it('getStreamDefs returns empty for unregistered commands', () => {
    const { program } = createCLI({ name: 't', version: '0.1.0', description: 't' });
    const sub = program.command('run');
    expect(getStreamDefs(sub)).toEqual([]);
  });
});
