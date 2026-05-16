/**
 * @module cli
 * @package @hop-top/kit
 *
 * Commander-based CLI factory that enforces the hop-top CLI contract.
 *
 * The hop-top CLI contract:
 *   - No `help` subcommand; help is exposed only as -h/--help flag.
 *   - `completion` is a management-group command (hidden from default help).
 *   - Version is a root-level option (-v/--version), NOT a subcommand.
 *   - All hop-top CLIs expose three global options on the root command:
 *       --format <fmt>   table | json | yaml  (default: "table")
 *       --quiet          suppress non-essential output
 *       --no-color       disable ANSI colour
 *   - Errors are printed to stderr in error color (unless --no-color is set).
 *
 * Requirements: Node >= 20, commander ^14.
 *
 * @example
 * ```ts
 * import { createCLI } from '@hop-top/kit/cli';
 * const { program, theme } = createCLI({ name: 'mytool', version: '1.0.0', description: 'My tool' });
 * program.command('run').action(() => { ... });
 * program.parse();
 * ```
 */

import { Command, Help } from 'commander';
import { parity } from './tui/parity.js';
import { registerOutputFlags } from './output/flags.js';
import './output/builtins.js';
export { HintSet, hintsEnabled, renderHints, active,
         registerUpgradeHints, registerVersionHints } from './hint.js';
export type { Hint, HintOptions } from './hint.js';

// ---------------------------------------------------------------------------
// Verbose count — stacking -V flags
// ---------------------------------------------------------------------------

const verboseCountMap = new WeakMap<Command, number>();

/** Read the accumulated -V count for a parsed command tree. */
export function verboseCount(cmd: Command): number {
  // Walk up to root to find the stored count.
  let c: Command | null = cmd;
  while (c) {
    const v = verboseCountMap.get(c);
    if (v !== undefined) return v;
    c = c.parent ?? null;
  }
  return 0;
}

// ---------------------------------------------------------------------------
// Named streams — registerStream / channel
// ---------------------------------------------------------------------------

interface StreamDef {
  name: string;
  description: string;
}

const streamDefsMap = new WeakMap<Command, StreamDef[]>();

/**
 * Register a named diagnostic stream on a command.
 * Adds a `--stream <names>` option (comma-separated) on first call.
 *
 * Parity note: Go uses StringSlice (repeatable --stream); TS and Python
 * use a single comma-separated string value.
 */
export function registerStream(
  cmd: Command, name: string, description: string,
): void {
  let defs = streamDefsMap.get(cmd);
  if (!defs) {
    defs = [];
    streamDefsMap.set(cmd, defs);
    // Add --stream option on first registration.
    cmd.option(
      '--stream <names>',
      'Enable diagnostic streams (comma-separated)',
    );
  }
  defs.push({ name, description });
}

/** Return registered stream definitions for a command. */
export function getStreamDefs(cmd: Command): StreamDef[] {
  return streamDefsMap.get(cmd) ?? [];
}

const noopWriter = { write(_s: string): boolean { return true; } };

/**
 * Return a writer for a named stream. When the stream is enabled via
 * `--stream`, writes go to stderr with a `[name] ` prefix. Otherwise
 * writes are discarded.
 */
export function channel(
  cmd: Command, name: string,
): { write(s: string): boolean } {
  const opts = cmd.opts();
  const enabled = typeof opts.stream === 'string'
    ? opts.stream.split(',').map((s: string) => s.trim()).includes(name)
    : false;
  if (!enabled) return noopWriter;
  return {
    write(s: string): boolean {
      // Split on newlines and prefix each line (parity with Go/Python).
      const lines = s.split('\n');
      for (const line of lines) {
        if (line) process.stderr.write(`[${name}] ${line}\n`);
      }
      return true;
    },
  };
}

// ---------------------------------------------------------------------------
// Palette
// ---------------------------------------------------------------------------

/**
 * Brand color pair used across a theme.
 * Hex strings (e.g. `"#7ED957"`).
 */
export interface Palette {
  /** Commands / primary accent. */
  command: string;
  /** Flags / secondary accent. */
  flag: string;
}

/**
 * Built-in Neon palette — vivid grass green + neon pink.
 * Mirrors Go's `Neon` palette in `kit/cli/theme.go`.
 */
export const Neon: Palette = {
  command: '#7ED957', // grass green
  flag: '#FF00FF',   // vivid neon pink
};

/**
 * Built-in Dark palette — softer lime + pink.
 * Mirrors Go's `Dark` palette in `kit/cli/theme.go`.
 */
export const Dark: Palette = {
  command: '#C1FF72', // lime
  flag: '#FF66C4',   // pink
};

// ---------------------------------------------------------------------------
// Theme
// ---------------------------------------------------------------------------

/**
 * Semantic color theme for CLI output.
 * Mirrors Go's `Theme` struct in `kit/cli/theme.go`.
 */
export interface Theme {
  palette: Palette;
  /** Primary accent — same as `palette.command`. */
  accent: string;
  /** Secondary accent — same as `palette.flag`. */
  secondary: string;
  /** Muted/dim color (CharmTone Squid). */
  muted: string;
  /** Error color (CharmTone Cherry). */
  error: string;
  /** Success color (CharmTone Guac). */
  success: string;
  /** Title/header color — mirrors fang's ColorScheme.Title (#FFFFFF). */
  title: string;
  /** Argument placeholder color — mirrors fang's ColorScheme.Argument (#B5E89B). */
  argument: string;
  /** Dimmed argument color — mirrors fang's ColorScheme.DimmedArgument (#8ABF6E). */
  dimmedArgument: string;
}

/**
 * Build a Theme from an optional accent override.
 * When `accent` is provided it replaces `palette.command`; otherwise
 * the Neon palette is used. Mirrors Go's `buildTheme` function.
 */
export function buildTheme(accent?: string): Theme {
  const palette: Palette = { ...Neon };
  if (accent) {
    palette.command = accent;
  }
  return {
    palette,
    accent:         palette.command,
    secondary:      palette.flag,
    muted:          '#858183', // CharmTone Squid
    error:          '#ED4A5E', // CharmTone Cherry
    success:        '#52CF84', // CharmTone Guac
    title:          '#FFFFFF', // fang ColorScheme.Title
    argument:       '#B5E89B', // fang ColorScheme.Argument
    dimmedArgument: '#8ABF6E', // fang ColorScheme.DimmedArgument
  };
}

// ---------------------------------------------------------------------------
// CLIConfig
// ---------------------------------------------------------------------------

/**
 * Configuration passed to {@link createCLI} to identify the program.
 */
export interface CLIConfig {
  /**
   * Binary name as invoked by the user (e.g. `"mytool"`).
   * Used as the Commander program name and in the version string output.
   */
  name: string;

  /**
   * Semver version string (e.g. `"1.2.3"`).
   * Exposed via `-v / --version`; do NOT include a leading `v`.
   */
  version: string;

  /**
   * One-line description shown in help output.
   */
  description: string;

  /**
   * Optional hex color string (e.g. `"#FF0000"`) used as the theme accent.
   * Omit to use the Neon palette default.
   */
  accent?: string;

  /** Opt-out built-in global flags. Zero value = all enabled. */
  disable?: {
    format?: boolean;
    quiet?: boolean;
    noColor?: boolean;
    hints?: boolean;
  };

  /** Command groups for partitioned help (e.g. COMMANDS vs MANAGEMENT). */
  groups?: Array<{
    id: string;       // e.g. "management"
    title: string;    // e.g. "MANAGEMENT"
    hidden?: boolean; // hidden from default --help
  }>;

  /** Extra tool-specific persistent flags on root command. */
  globals?: Array<{
    name: string;      // long name without -- (e.g. "verbose")
    short?: string;    // single char optional (e.g. "v")
    usage: string;     // description shown in --help
    default?: string;  // string default; empty = no default
  }>;

  /**
   * Root --help layout overrides.
   * Defaults are loaded from contracts/parity/parity.json.
   */
  help?: {
    /**
     * Appended to `description` as a separate paragraph when non-empty.
     * Empty = no extra block.
     */
    disclaimer?: string;
    /**
     * Section rendering order using fang vocabulary
     * (e.g. `["commands", "flags"]`). Omit to use parity.json default.
     */
    sectionOrder?: string[];
    showAliases?: boolean;
  };
}

// ---------------------------------------------------------------------------
// createCLI result
// ---------------------------------------------------------------------------

/**
 * Result of {@link createCLI}.
 */
export interface CLIResult {
  /** Configured Commander root program. */
  program: Command;
  /** Theme derived from the config accent (or Neon default). */
  theme: Theme;
}

// ---------------------------------------------------------------------------
// Command groups
// ---------------------------------------------------------------------------

/** Map Command → group ID for partitioned help rendering. */
const commandGroupMap = new WeakMap<Command, string>();

/** Assign a command to a named group (used in formatHelp partitioning). */
export function setCommandGroup(cmd: Command, groupId: string): void {
  commandGroupMap.set(cmd, groupId);
}

/** Retrieve the group ID for a command (undefined = default group). */
export function getCommandGroup(cmd: Command): string | undefined {
  return commandGroupMap.get(cmd);
}

// ---------------------------------------------------------------------------
// Help theming
// ---------------------------------------------------------------------------

/** Parse a hex color string like "#7ED957" into [r, g, b]. */
function hexToRgb(hex: string): [number, number, number] {
  const h = hex.replace('#', '');
  return [
    parseInt(h.slice(0, 2), 16),
    parseInt(h.slice(2, 4), 16),
    parseInt(h.slice(4, 6), 16),
  ];
}

/** Wrap text in an ANSI 24-bit foreground color SGR sequence. */
function ansi(hex: string, text: string): string {
  const [r, g, b] = hexToRgb(hex);
  return `\x1b[38;2;${r};${g};${b}m${text}\x1b[0m`;
}

/** Bold + color wrapper. */
function ansiBold(hex: string, text: string): string {
  const [r, g, b] = hexToRgb(hex);
  return `\x1b[1;38;2;${r};${g};${b}m${text}\x1b[0m`;
}

/**
 * Apply hop-top brand colors to Commander's help output.
 *
 * Uses Commander's structured Help API (optionTerm, subcommandTerm, etc.)
 * to color each element independently — no regex post-processing on
 * description text.
 *
 * Color mapping (mirrors Go's brandColorScheme):
 *   - Section headers: #FFFFFF bold
 *   - Flag names: theme.secondary (#FF00FF)
 *   - Command names: theme.accent (#7ED957)
 *   - Arguments (<angle>): theme.argument (#B5E89B)
 *   - Dimmed args ([optional]): theme.dimmedArgument (#8ABF6E)
 *   - Descriptions: theme.muted (#858183)
 *   - Usage program name: theme.accent
 */
export function applyHelpTheme(
  program: Command,
  theme: Theme,
  noColor: boolean,
  sectionOrder?: string[],
  showAliases?: boolean,
  groups?: CLIConfig['groups'],
): void {
  // Determine no-color state lazily at render time.
  const isNoColor = (): boolean =>
    noColor ||
    process.argv.includes('--no-color') ||
    process.env['NO_COLOR'] !== undefined;

  // Tell Commander our output supports color so it won't strip ANSI.
  // Our style hooks already handle no-color suppression.
  program.configureOutput({
    ...((program as any)._outputConfiguration ?? {}),
    getOutHasColors: () => !isNoColor(),
    getErrHasColors: () => !isNoColor(),
  });

  program.configureHelp({
    // --- V14 style hooks: delegate coloring here ---
    styleTitle(str: string): string {
      return isNoColor() ? str : ansiBold(theme.title, str);
    },
    styleUsage(str: string): string {
      if (isNoColor()) return str;
      // Color each word: command names as accent, args/options dimmed.
      return str.split(' ').map(word => {
        if (word === '[options]' || word === '[--flags]')
          return ansi(theme.dimmedArgument, word);
        if (word[0] === '[' || word[0] === '<')
          return ansi(theme.argument, word);
        return ansi(theme.accent, word);
      }).join(' ');
    },
    styleCommandText(str: string): string {
      return isNoColor() ? str : ansi(theme.accent, str);
    },
    styleSubcommandText(str: string): string {
      return isNoColor() ? str : ansi(theme.accent, str);
    },
    styleOptionText(str: string): string {
      return isNoColor() ? str : ansi(theme.secondary, str);
    },
    styleArgumentText(str: string): string {
      return isNoColor() ? str : ansi(theme.argument, str);
    },
    styleDescriptionText(str: string): string {
      return isNoColor() ? str : ansi(theme.muted, str);
    },

    // Color each part of option term separately (short, long, arg).
    styleOptionTerm(str: string): string {
      if (isNoColor()) return str;
      return str.split(' ').map(word => {
        if (word === ',') return word;
        if (word.startsWith('<') || word.startsWith('['))
          return ansi(theme.argument, word);
        if (word.endsWith(','))
          return ansi(theme.secondary, word.slice(0, -1)) + ',';
        return ansi(theme.secondary, word);
      }).join(' ');
    },

    // Color subcommand term parts: name in accent, args dimmed.
    styleSubcommandTerm(str: string): string {
      if (isNoColor()) return str;
      const words = str.split(' ');
      return words.map((word, i) => {
        if (i === 0) return ansi(theme.accent, word);
        if (word.startsWith('<') || word.startsWith('['))
          return ansi(theme.dimmedArgument, word);
        return ansi(theme.accent, word);
      }).join(' ');
    },

    // --- Term overrides ---
    subcommandTerm(cmd: Command): string {
      // Match Go/fang: name <args> [--flags] or name [command]
      const args = cmd.registeredArguments
        .map((a: any) => a.required ? `<${a.name()}>` : `[${a.name()}]`)
        .join(' ');
      const hasSubcmds = cmd.commands.length > 0;
      let term = cmd.name();
      if (args) term += ' ' + args;
      if (hasSubcmds) term += ' [command]';
      else if (cmd.options.length) term += ' [--flags]';
      return term;
    },

    // --- Custom formatHelp for group partitioning + section ordering ---
    formatHelp(cmd: Command, helper: Help): string {
      const termWidth = helper.padWidth(cmd, helper);

      function callFormatItem(term: string, description: string): string {
        return helper.formatItem(term, termWidth, description, helper);
      }

      const output: string[] = [];

      // Usage line.
      output.push(
        `${helper.styleTitle('Usage:')} ${helper.styleUsage(helper.commandUsage(cmd))}`,
      );
      output.push('');

      // Description.
      const desc = helper.commandDescription(cmd);
      if (desc) {
        output.push(helper.styleCommandDescription(desc));
        output.push('');
      }

      // Collect sections in parity order.
      const helpSections: { name: string; items: string[] }[] = [];

      // Check --help-all and --help-<id> from argv.
      const helpAll = process.argv.includes('--help-all');
      let helpGroupFilter: string | undefined;
      for (const arg of process.argv) {
        const m = arg.match(/^--help-(.+)$/);
        if (m && m[1] !== 'all') {
          helpGroupFilter = m[1];
          break;
        }
      }

      // Build group lookup: id -> { title, hidden }.
      const groupDefs = new Map<string, { title: string; hidden: boolean }>();
      for (const g of groups ?? []) {
        groupDefs.set(g.id, { title: g.title, hidden: g.hidden ?? false });
      }

      // Commands — partition by group.
      const allCmds = helper.visibleCommands(cmd)
        .filter((sc: Command) => sc.name() !== 'help');
      const defaultCmds: Command[] = [];
      const groupedCmds = new Map<string, Command[]>();

      for (const sc of allCmds) {
        const gid = getCommandGroup(sc);
        if (gid && groupDefs.has(gid)) {
          let arr = groupedCmds.get(gid);
          if (!arr) { arr = []; groupedCmds.set(gid, arr); }
          arr.push(sc);
        } else {
          defaultCmds.push(sc);
        }
      }

      // Build formatted items for a list of commands.
      const buildCmdItems = (cmds: Command[]): string[] => {
        return cmds.map((sc: Command) => {
          let descr = helper.subcommandDescription(sc);
          if (showAliases && sc.aliases().length > 0) {
            descr += ` (aliases: ${sc.aliases().join(', ')})`;
          }
          return callFormatItem(
            helper.styleSubcommandTerm(helper.subcommandTerm(sc)),
            helper.styleSubcommandDescription(descr),
          );
        });
      };

      if (defaultCmds.length > 0 && !helpGroupFilter) {
        helpSections.push({
          name: 'commands', items: buildCmdItems(defaultCmds),
        });
      }

      // Group sections (respecting hidden/helpAll/helpGroupFilter).
      for (const [gid, cmds] of groupedCmds) {
        if (helpGroupFilter && gid !== helpGroupFilter) continue;
        const def = groupDefs.get(gid)!;
        if (!helpGroupFilter && def.hidden && !helpAll) continue;
        if (cmds.length > 0) {
          helpSections.push({ name: gid, items: buildCmdItems(cmds) });
        }
      }

      // Flags section — use v14's formatItem for consistent padding.
      const opts = helper.visibleOptions(cmd);
      if (opts.length > 0) {
        const flagItems = opts.map((o: any) =>
          callFormatItem(
            helper.styleOptionTerm(helper.optionTerm(o)),
            helper.styleOptionDescription(helper.optionDescription(o)),
          ),
        );
        helpSections.push({ name: 'flags', items: flagItems });
      }

      // Render sections in parity order.
      const sectionMap = new Map(helpSections.map(s => [s.name, s]));
      const effectiveOrder = (sectionOrder && sectionOrder.length > 0)
        ? sectionOrder : parity.help.section_order;
      const sectionTitles = parity.help.sections ?? {};

      const resolveTitle = (name: string): string => {
        const gdef = groupDefs.get(name);
        if (gdef) return gdef.title;
        return sectionTitles[name]?.title
          ?? name.charAt(0).toUpperCase() + name.slice(1);
      };

      // Render sections: flags always last, after all command groups.
      for (const name of effectiveOrder) {
        if (name === 'flags') continue;
        const sec = sectionMap.get(name);
        if (!sec) continue;
        output.push(helper.styleTitle(`${resolveTitle(name)}:`));
        output.push(...sec.items);
        output.push('');
      }
      for (const sec of helpSections) {
        if (sec.name === 'flags') continue;
        if (effectiveOrder.includes(sec.name)) continue;
        output.push(helper.styleTitle(`${resolveTitle(sec.name)}:`));
        output.push(...sec.items);
        output.push('');
      }
      // FLAGS last.
      const flagsSec = sectionMap.get('flags');
      if (flagsSec) {
        output.push(helper.styleTitle(`${resolveTitle('flags')}:`));
        output.push(...flagsSec.items);
        output.push('');
      }

      // STREAMS section — show registered streams for this command.
      const defs = getStreamDefs(cmd);
      if (defs.length > 0) {
        const styleTerm = (helper as any).styleOptionTerm?.bind(helper)
          ?? ((s: string) => s);
        const styleDesc = (helper as any).styleDescriptionText?.bind(helper)
          ?? ((s: string) => s);
        output.push(helper.styleTitle('STREAMS:'));
        for (const d of defs) {
          output.push(callFormatItem(styleTerm(d.name), styleDesc(d.description)));
        }
        output.push('');
      }

      return output.join('\n') + '\n';
    },
  });
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

/**
 * Creates a Commander `Command` (root program) pre-configured to the hop-top CLI contract.
 *
 * Contract enforced by this factory (callers must NOT override these):
 *   - No `help` subcommand; -h/--help flag only.
 *   - `completion` available as management-group command (hidden by default).
 *   - `-v, --version` is a root option (not a subcommand); prints `<name> v<version>` and exits.
 *   - Global options added to the returned program:
 *       `--format <fmt>`  Output format — `"table"` | `"json"` | `"yaml"` (default `"table"`)
 *       `--quiet`         Suppress non-essential output (boolean, default `false`)
 *       `--no-color`      Disable ANSI colour (boolean, default `false`)
 *   - `showHelpAfterError` is enabled; unknown commands print help to stderr.
 *   - Errors are written to stderr in `theme.error` color unless `--no-color` is active.
 *
 * Requires commander ^14. Do not add `-v/--version`, `--format`, `--quiet`, or
 * `--no-color` again on the returned program — they are already registered.
 *
 * @param cfg - Program identity (name, version, description, optional accent). See {@link CLIConfig}.
 * @returns   `{ program, theme }` — program ready for sub-commands; theme for TUI components.
 *
 * @example
 * ```ts
 * const { program, theme } = createCLI({ name: 'hop', version: '2.0.0', description: 'hop CLI' });
 * program.command('deploy').action(() => { ... });
 * program.parse();
 * ```
 */
export function createCLI(cfg: CLIConfig): CLIResult {
  const theme = buildTheme(cfg.accent);

  const desc = cfg.help?.disclaimer
    ? `${cfg.description}\n\n${cfg.help.disclaimer}`
    : cfg.description;

  // Format version as "<name> v<version>" per hop-top contract.
  const normalizedVersion = cfg.version.startsWith('v') ? cfg.version : `v${cfg.version}`;
  const versionOutput = `${cfg.name} ${normalizedVersion}`;

  const program = new Command(cfg.name)
    .description(desc)
    .version(versionOutput, '-v, --version', `Print ${cfg.name} version and exit`)
    .helpOption('-h, --help', 'Display help')
    .addHelpCommand(false)
    .showHelpAfterError(true);

  // Output flag suite: --format + --format-opt + --format-help +
  // --cols/--columns + --template + --output|-o. disable.format toggles
  // the whole suite for callers that pipe stdout as part of a contract.
  if (!cfg.disable?.format) {
    registerOutputFlags(program);
  }
  if (!cfg.disable?.quiet) {
    program.option('--quiet', 'Suppress non-essential output', false);
  }
  if (!cfg.disable?.noColor) {
    program.option('--no-color', 'Disable ANSI colour', false);
  }
  if (!cfg.disable?.hints) {
    program.option('--no-hints', 'Suppress next-step hints after command output', false);
  }

  // -V / --verbose: accumulator pattern for stacking (-VV = 2).
  program.option(
    '-V, --verbose', 'Increase verbosity',
    (_: string, prev: number) => prev + 1, 0,
  );
  // Store count after parse; quiet overrides verbose.
  program.hook('preAction', (thisCmd) => {
    const opts = thisCmd.opts();
    const count = opts.quiet ? 0 : (opts.verbose ?? 0);
    verboseCountMap.set(thisCmd, count);
  });

  if (cfg.groups?.some(g => g.hidden)) {
    program.option('--help-all', 'Show all commands including hidden groups');
    program.on('option:help-all', () => {
      program.help();
    });
  }

  // Per-group help flags: --help-<id> for each registered group.
  for (const g of cfg.groups ?? []) {
    program.option(`--help-${g.id}`, `Show ${g.title} commands`);
    program.on(`option:help-${g.id}`, () => {
      program.help();
    });
  }

  // help <group> subcommand (hidden from default help).
  if (cfg.groups && cfg.groups.length > 0) {
    const groupIds = cfg.groups.map(g => g.id);
    const helpCmd = program.command('help [group]')
      .description('Show help for a command group')
      .action((group?: string) => {
        if (group === 'all') {
          process.argv.push('--help-all');
          program.help();
        } else if (group && groupIds.includes(group)) {
          process.argv.push(`--help-${group}`);
          program.help();
        } else {
          program.help();
        }
      });
    // Hide from default help output.
    (helpCmd as any).hideHelp = true;
    (helpCmd as any)._hidden = true;
  }

  for (const g of cfg.globals ?? []) {
    const flag = g.short ? `-${g.short}, --${g.name} <value>` : `--${g.name} <value>`;
    program.option(flag, g.usage, g.default ?? '');
  }

  // Apply brand color scheme + section order to --help output.
  // noColor is read lazily at render time inside applyHelpTheme.
  applyHelpTheme(program, theme, false, cfg.help?.sectionOrder,
    cfg.help?.showAliases, cfg.groups);

  // Styled error output: merge with existing outputConfiguration to
  // preserve getOutHasColors/getErrHasColors set by applyHelpTheme.
  const existing = (program as any)._outputConfiguration ?? {};
  program.configureOutput({
    ...existing,
    outputError(str: string, write: (s: string) => void) {
      const nc = process.argv.includes('--no-color') ||
        process.env['NO_COLOR'] !== undefined;
      if (nc) {
        write(str);
      } else {
        write(ansi(theme.error, str));
      }
    },
  });

  return { program, theme };
}
