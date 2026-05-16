/**
 * terminal.ts — browser adapter for spaced.
 *
 * Captures stdout/stderr from Commander-based spaced commands by shimming
 * process and console, then routing the parsed argv through the same command
 * handlers used in the CLI. No Node APIs reach the browser.
 */

import { Command } from 'commander';
import { MISSIONS, VEHICLES, COMPETITORS, DAEMONS, findMission, findVehicle, findCompetitor, findDaemon, pick } from '../ts/data';
import { missionCommand }    from '../ts/commands/mission';
import { launchCommand }     from '../ts/commands/launch';
import { abortCommand }      from '../ts/commands/abort';
import { telemetryCommand }  from '../ts/commands/telemetry';
import { fleetCommand }      from '../ts/commands/fleet';
import { starshipCommand }   from '../ts/commands/starship';
import { elonCommand }       from '../ts/commands/elon';
import { ipoCommand }        from '../ts/commands/ipo';
import { competitorCommand } from '../ts/commands/competitor';
import { daemonCommand }     from '../ts/commands/daemon';
import { buildTheme, applyHelpTheme } from '../../../sdk/ts/src/cli';

// ---------------------------------------------------------------------------
// Output capture
// ---------------------------------------------------------------------------

/** Execute a spaced command string and return its output (ANSI stripped). */
export async function runCommand(input: string): Promise<{ out: string; err: string; code: number }> {
  const lines: string[] = [];
  const errLines: string[] = [];
  let exitCode = 0;

  // Shim console.log / console.error within this call scope.
  const origLog = console.log;
  const origErr = console.error;
  console.log  = (...args: unknown[]) => lines.push(args.map(String).join(' '));
  console.error = (...args: unknown[]) => errLines.push(args.map(String).join(' '));

  // Shim process.stdout.write / process.stderr.write.
  const origStdout = (process as NodeJS.Process & { stdout: { write: (...a: unknown[]) => boolean } }).stdout?.write;
  const origStderr = (process as NodeJS.Process & { stderr: { write: (...a: unknown[]) => boolean } }).stderr?.write;
  if (process.stdout) {
    (process.stdout as unknown as { write: (s: string) => void }).write = (s: string) => lines.push(s);
  }
  if (process.stderr) {
    (process.stderr as unknown as { write: (s: string) => void }).write = (s: string) => errLines.push(s);
  }

  // Shim process.exit.
  const origExit = process.exit;
  (process as unknown as { exit: (code?: number) => never }).exit = (code?: number) => {
    exitCode = code ?? 0;
    throw new ExitSignal(code ?? 0);
  };

  try {
    const argv = parseInput(input);
    const program = buildProgram();
    await program.parseAsync(['node', 'spaced', ...argv]);
  } catch (e) {
    if (e instanceof ExitSignal) {
      exitCode = e.code;
    } else {
      errLines.push(String(e));
      exitCode = 1;
    }
  } finally {
    console.log   = origLog;
    console.error = origErr;
    if (process.stdout && origStdout) {
      (process.stdout as unknown as { write: unknown }).write = origStdout;
    }
    if (process.stderr && origStderr) {
      (process.stderr as unknown as { write: unknown }).write = origStderr;
    }
    (process as unknown as { exit: unknown }).exit = origExit;
  }

  return {
    out:  stripANSI(lines.join('\n')),
    err:  stripANSI(errLines.join('\n')),
    code: exitCode,
  };
}

// ---------------------------------------------------------------------------
// Program factory (browser-safe, no fang, no viper)
// ---------------------------------------------------------------------------

function buildProgram(): Command {
  const theme = buildTheme();

  const program = new Command('spaced')
    .description('Satirical SpaceX CLI historian')
    .version('spaced 0.1.0', '-v, --version', 'Print spaced version and exit')
    .helpOption('-h, --help', 'Display help')
    .addHelpCommand(false)
    .showHelpAfterError(false);

  program.option('--format <fmt>', 'Output format: table, json, yaml', 'table');
  program.option('--quiet', 'Suppress non-essential output', false);
  program.option('--no-color', 'Disable ANSI colour', false);

  const DISCLAIMER = `
Not affiliated with, endorsed by, or in any way authorized by SpaceX,
Elon Musk, DOGE, NASA, the FAA, or the Starman mannequin currently past Mars.
We would, however, accept a sponsorship (https://github.com/sponsors/hop-top).
Cash, Starlink credits, or a ride on the next Crew Dragon all acceptable.
`;

  const origHelp = program.helpInformation.bind(program);
  program.helpInformation = () => origHelp() + DISCLAIMER;

  applyHelpTheme(program, theme, true); // no-color in browser (we do our own styling)

  program.addCommand(missionCommand());
  program.addCommand(launchCommand());
  program.addCommand(abortCommand());
  program.addCommand(telemetryCommand());
  program.addCommand(fleetCommand());
  program.addCommand(starshipCommand());
  program.addCommand(elonCommand());
  program.addCommand(ipoCommand());
  program.addCommand(competitorCommand());
  program.addCommand(daemonCommand());

  return program;
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

class ExitSignal extends Error {
  constructor(public readonly code: number) { super(`exit ${code}`); }
}

/** Tokenise a shell-like input string (handles quoted args). */
function parseInput(input: string): string[] {
  const tokens: string[] = [];
  let cur = '';
  let inQ = false;
  let qChar = '';
  for (const ch of input.trim()) {
    if (inQ) {
      if (ch === qChar) { inQ = false; }
      else { cur += ch; }
    } else if (ch === '"' || ch === "'") {
      inQ = true; qChar = ch;
    } else if (ch === ' ') {
      if (cur) { tokens.push(cur); cur = ''; }
    } else {
      cur += ch;
    }
  }
  if (cur) tokens.push(cur);
  return tokens;
}

/** Strip ANSI SGR escape sequences. */
function stripANSI(s: string): string {
  return s.replace(/\x1b\[[^m]*m/g, '');
}

// ---------------------------------------------------------------------------
// Curated demo sequence (plays on page load)
// ---------------------------------------------------------------------------

export const DEMO_SEQUENCE: string[] = [
  'spaced --help',
  'spaced mission list',
  'spaced mission inspect starman',
  'spaced daemon list',
  'spaced daemon stop funding-secured',
  'spaced elon status',
  'spaced starship status',
  'spaced ipo status',
  'spaced competitor compare boeing',
];

// ---------------------------------------------------------------------------
// Suggestion chips (shown below terminal)
// ---------------------------------------------------------------------------

export const SUGGESTIONS: string[] = [
  'mission list',
  'mission inspect SN8',
  'starship status',
  'starship history',
  'elon status',
  'ipo status',
  'daemon list',
  'daemon status funding-secured',
  'daemon stop --all',
  'competitor compare "Blue Origin"',
  'fleet list',
  'fleet vehicle inspect "Falcon 9"',
  'launch starman --dry-run',
  'launch SN8 --payload cargo,crew --orbit leo',
  'mission list --format json',
  'telemetry get starman',
];
