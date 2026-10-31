/**
 * @module completion
 * @package @hop-top/kit
 *
 * Shell completion for Commander-based CLIs, in cobra's protocol so a
 * TS kit CLI completes like a Go or Rust one
 * (`contracts/completion-v1`, Go canonical):
 *
 * - `completion bash|zsh|fish|powershell [--no-descriptions]` writes
 *   cobra v1.10.2's scripts byte for byte ({@link completionCommand}).
 * - The scripts call back into the hidden `__complete` /
 *   `__completeNoDesc` entry point. {@link installCompletionRequest}
 *   (done by `createCLI` and {@link completionCommand}) answers it
 *   before commander parses, so no hook or action runs on tab. It walks
 *   the live command tree: subcommands, aliases, each command's own and
 *   inherited flags, choices and the completers bound with
 *   {@link bindFlag} / {@link bindArgs}.
 *
 * Wire format: one completion per line (`value`, or
 * `value<TAB>description`), then `:<directive>`; stderr carries
 * `Completion ended with directive: <name>`. Exit code 0.
 *
 * Commander has no public accessor for a few facts read here; they are
 * typed locally in {@link CommanderInternals} (commander 15).
 */

import { appendFileSync } from 'node:fs';
import { Command, Option } from 'commander';

import { SCRIPT_TEMPLATES } from './completion-scripts';
import {
  type Completer,
  type CompletionContext,
  directiveName,
  funcCompleter,
  resolveCompletions,
  ShellCompDirective,
} from './completion-values';

/** Name of the hidden command a shell script calls for completions. */
export const REQUEST_CMD = '__complete';
/** Name of the hidden command that returns completions without descriptions. */
export const NO_DESC_REQUEST_CMD = '__completeNoDesc';

/** A shell `completion` writes a script for. */
export type Shell = 'bash' | 'zsh' | 'fish' | 'powershell';
/** Every shell, in the order the `completion` command lists them. */
export const SHELLS: readonly Shell[] = ['bash', 'zsh', 'fish', 'powershell'];

/** commander 15 private members this module reads (no public accessor). */
interface CommanderInternals {
  _hidden?: boolean;
  _enablePositionalOptions?: boolean;
  _versionOptionName?: string;
  _getHelpOption?(): Option | null;
}

function internals(c: Command): CommanderInternals {
  return c as unknown as CommanderInternals;
}

// Per-command state lives on the command under registry symbols, so two
// copies of this module (separate bundles) still share it.
const FLAG_COMPLETERS = Symbol.for('@hop-top/kit/completion/flags');
const ARGS_COMPLETER = Symbol.for('@hop-top/kit/completion/args');
const ROOT_ALIASES = Symbol.for('@hop-top/kit/completion/aliases');
const BUILTIN = Symbol.for('@hop-top/kit/completion/builtin');
const INSTALLED = Symbol.for('@hop-top/kit/completion/installed');

type Tagged = Command & {
  [FLAG_COMPLETERS]?: Map<string, Completer>;
  [ARGS_COMPLETER]?: Completer;
  [ROOT_ALIASES]?: Map<string, string>;
  [BUILTIN]?: boolean;
  [INSTALLED]?: boolean;
};

// ---------------------------------------------------------------------------
// Scripts
// ---------------------------------------------------------------------------

/** cobra's `<PROGRAM>_<SUFFIX>` variable name. */
function configEnvVar(name: string, suffix: string): string {
  return `${name}_${suffix}`.toUpperCase().replace(/[^A-Z0-9_]/g, '_');
}

/** The `completion <shell>` script for program `name`, cobra's byte for byte. */
export function renderCompletionScript(
  shell: Shell,
  name: string,
  includeDescriptions = true,
): string {
  const vars: Record<string, string> = {
    NAME_PAD36: name.padEnd(36),
    // Shell identifiers cannot hold '-' or ':'.
    NAME_VAR: name.replace(/[-:]/g, '_'),
    ACTIVE_HELP_VAR: configEnvVar(name, 'ACTIVE_HELP'),
    COMP_CMD: includeDescriptions ? REQUEST_CMD : NO_DESC_REQUEST_CMD,
    NAME: name,
  };
  return SCRIPT_TEMPLATES[shell].replace(
    /@@(NAME_PAD36|NAME_VAR|ACTIVE_HELP_VAR|COMP_CMD|NAME)@@/g,
    (_m, k: string) => vars[k]!,
  );
}

/** Generate a bash completion script (cobra V2; needs bash-completion). */
export function generateBashCompletion(program: Command, includeDescriptions = true): string {
  return renderCompletionScript('bash', program.name(), includeDescriptions);
}

/** Generate a zsh completion script for the given program. */
export function generateZshCompletion(program: Command, includeDescriptions = true): string {
  return renderCompletionScript('zsh', program.name(), includeDescriptions);
}

/** Generate a fish completion script for the given program. */
export function generateFishCompletion(program: Command, includeDescriptions = true): string {
  return renderCompletionScript('fish', program.name(), includeDescriptions);
}

/** Generate a PowerShell completion script for the given program. */
export function generatePowerShellCompletion(
  program: Command,
  includeDescriptions = true,
): string {
  return renderCompletionScript('powershell', program.name(), includeDescriptions);
}

function shellHelp(sh: Shell, p: string): string {
  switch (sh) {
    case 'bash':
      return `
This script depends on the 'bash-completion' package.
If it is not installed already, you can install it via your OS's package manager.

To load completions in your current shell session:

\tsource <(${p} completion bash)

To load completions for every new session, execute once:

#### Linux:

\t${p} completion bash > /etc/bash_completion.d/${p}

#### macOS:

\t${p} completion bash > $(brew --prefix)/etc/bash_completion.d/${p}

You will need to start a new shell for this setup to take effect.
`;
    case 'zsh':
      return `
If shell completion is not already enabled in your environment you will need
to enable it.  You can execute the following once:

\techo "autoload -U compinit; compinit" >> ~/.zshrc

To load completions in your current shell session:

\tsource <(${p} completion zsh)

To load completions for every new session, execute once:

#### Linux:

\t${p} completion zsh > "\${fpath[1]}/_${p}"

#### macOS:

\t${p} completion zsh > $(brew --prefix)/share/zsh/site-functions/_${p}

You will need to start a new shell for this setup to take effect.
`;
    case 'fish':
      return `
To load completions in your current shell session:

\t${p} completion fish | source

To load completions for every new session, execute once:

\t${p} completion fish > ~/.config/fish/completions/${p}.fish

You will need to start a new shell for this setup to take effect.
`;
    case 'powershell':
      return `
To load completions in your current shell session:

\t${p} completion powershell | Out-String | Invoke-Expression

To load completions for every new session, add the output of the above command
to your powershell profile.
`;
  }
}

/** Completes nothing and no files: cobra's `NoFileCompletions`. */
const NO_FILE_COMPLETIONS = funcCompleter(() => []);

/**
 * cobra's `completion` command: `completion bash|zsh|fish|powershell
 * [--no-descriptions]`, each printing the script to stdout. Add it to
 * the program (kit puts it in the hidden `management` group). It also
 * installs the `__complete` entry point on `program`.
 */
export function completionCommand(program: Command): Command {
  installCompletionRequest(program);
  const cmd = new Command('completion')
    .description('Generate the autocompletion script for the specified shell');
  (cmd as Tagged)[BUILTIN] = true;
  bindArgs(cmd, NO_FILE_COMPLETIONS);

  for (const sh of SHELLS) {
    const leaf = cmd.command(sh)
      .description(`Generate the autocompletion script for ${sh}`)
      .option('--no-descriptions', 'disable completion descriptions')
      .addHelpText('after', shellHelp(sh, program.name()))
      .action((opts: { descriptions?: boolean }) => {
        process.stdout.write(
          renderCompletionScript(sh, program.name(), opts.descriptions !== false),
        );
      });
    bindArgs(leaf, NO_FILE_COMPLETIONS);
  }
  return cmd;
}

// ---------------------------------------------------------------------------
// Binding
// ---------------------------------------------------------------------------

/**
 * Binds `completer` as the value completion of `cmd`'s own flag `flag`
 * (long name, with or without `--`). An unknown flag is silent and the
 * first binding wins, as with cobra's `RegisterFlagCompletionFunc`. A
 * flag subcommands inherit is bound on the command declaring it.
 */
export function bindFlag(cmd: Command, flag: string, completer: Completer): void {
  const name = flag.replace(/^--?/, '');
  if (!cmd.options.some(o => o.long === `--${name}`)) return;
  const t = cmd as Tagged;
  const m = (t[FLAG_COMPLETERS] ??= new Map());
  if (!m.has(name)) m.set(name, completer);
}

/** Binds `completer` as the completion of `cmd`'s positional arguments; replaces an earlier binding. */
export function bindArgs(cmd: Command, completer: Completer): void {
  (cmd as Tagged)[ARGS_COMPLETER] = completer;
}

/**
 * Records `name` as a root-level alias of the command at `target` (a
 * space-separated path), so root argument completion offers it as
 * `name<TAB>alias for <target>`, and a root subcommand named `name`
 * completes as a flagless stand-in. `bridgeAliases` calls this for
 * every alias but a visible or custom-dispatch proxy, which completes
 * as an ordinary subcommand.
 */
export function registerRootAlias(root: Command, name: string, target: string): void {
  const t = root as Tagged;
  (t[ROOT_ALIASES] ??= new Map()).set(name, target.trim().split(/\s+/).join(' '));
}

// ---------------------------------------------------------------------------
// Request entry point
// ---------------------------------------------------------------------------

/** Where a request writes. */
export interface CompletionIO {
  out(s: string): void;
  err(s: string): void;
}

const stdio: CompletionIO = {
  out: s => { process.stdout.write(s); },
  err: s => { process.stderr.write(s); },
};

type ParseOptions = { from?: string };

/** The user words of `argv`, as commander's `_prepareUserArgs` finds them. */
function userArgs(argv: readonly string[] | undefined, opts?: ParseOptions): string[] | null {
  let from = opts?.from;
  if (argv === undefined && from === undefined) {
    if (process.versions?.electron) from = 'electron';
    const ea = process.execArgv ?? [];
    if (['-e', '--eval', '-p', '--print'].some(f => ea.includes(f))) from = 'eval';
  }
  const a = argv ?? process.argv;
  switch (from) {
    case undefined:
    case 'node': return a.slice(2);
    case 'electron': return (process as { defaultApp?: boolean }).defaultApp ? a.slice(2) : a.slice(1);
    case 'user': return a.slice(0);
    case 'eval': return a.slice(1);
    default: return null;
  }
}

async function serve(program: Command, words: string[], noDesc: boolean): Promise<void> {
  if (words.length === 0) {
    stdio.err(`Error: requires at least 1 arg(s), only received 0\n`);
    process.exitCode = 1;
    return;
  }
  try {
    await completeRequest(program, words, noDesc);
  } catch (e) {
    stdio.err(`Error: ${e instanceof Error ? e.message : String(e)}\n`);
    process.exitCode = 1;
  }
}

/**
 * Makes `program.parse` / `parseAsync` answer `__complete` and
 * `__completeNoDesc` requests themselves, before commander parses, so
 * no hook, option handler or action runs on tab. Idempotent.
 */
export function installCompletionRequest(program: Command): void {
  const t = program as Tagged;
  if (t[INSTALLED]) return;
  t[INSTALLED] = true;
  const request = (argv?: readonly string[], opts?: ParseOptions) => {
    const words = userArgs(argv, opts);
    const first = words?.[0];
    if (first !== REQUEST_CMD && first !== NO_DESC_REQUEST_CMD) return null;
    return serve(program, words!.slice(1), first === NO_DESC_REQUEST_CMD);
  };
  const parse = program.parse.bind(program);
  const parseAsync = program.parseAsync.bind(program);
  program.parse = ((argv?: readonly string[], opts?: ParseOptions) => {
    const pending = request(argv, opts);
    if (!pending) return parse(argv, opts as never);
    void pending;
    return program;
  }) as typeof program.parse;
  program.parseAsync = (async (argv?: readonly string[], opts?: ParseOptions) => {
    const pending = request(argv, opts);
    if (!pending) return parseAsync(argv, opts as never);
    await pending;
    return program;
  }) as typeof program.parseAsync;
}

/**
 * Answers a completion request: `words` are the words after
 * `__complete` (the last is the word being completed). Writes one
 * completion per line, then `:<directive>`, to `io.out`, and the
 * directive's name to `io.err`, in cobra's format.
 */
export async function completeRequest(
  program: Command,
  words: string[],
  noDesc = false,
  io: CompletionIO = stdio,
): Promise<void> {
  const { completions, directive, error } = await getCompletions(program, words);
  if (error) compError(io, error);
  if (!noDesc) {
    const v = envConfig(program.name(), 'COMPLETION_DESCRIPTIONS');
    const b = v === undefined ? undefined : parseBool(v);
    if (b !== undefined) noDesc = !b;
  }
  for (let c of completions) {
    if (noDesc) c = c.split('\t')[0]!;
    // First line only: a description with a line break would read as
    // further completions.
    c = c.split('\n')[0]!;
    io.out(`${goTrimSpace(c)}\n`);
  }
  io.out(`:${directive}\n`);
  io.err(`Completion ended with directive: ${directiveName(directive)}\n`);
}

/** cobra `getEnvConfig`: `<PROGRAM>_<SUFFIX>`, else `COBRA_<SUFFIX>`. */
function envConfig(prog: string, suffix: string): string | undefined {
  for (const k of [configEnvVar(prog, suffix), configEnvVar('COBRA', suffix)]) {
    const v = process.env[k];
    if (v) return v;
  }
  return undefined;
}

/** Go `strconv.ParseBool`. */
function parseBool(v: string): boolean | undefined {
  if (['1', 't', 'T', 'TRUE', 'true', 'True'].includes(v)) return true;
  if (['0', 'f', 'F', 'FALSE', 'false', 'False'].includes(v)) return false;
  return undefined;
}

/** Go `strings.TrimSpace`. */
function goTrimSpace(s: string): string {
  const ws = '[\\t\\n\\v\\f\\r \\u0085\\u00a0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000]';
  return s.replace(new RegExp(`^${ws}+|${ws}+$`, 'g'), '');
}

/** cobra `CompErrorln`: stderr, plus `$BASH_COMP_DEBUG_FILE` when set. */
function compError(io: CompletionIO, msg: string): void {
  const line = `[Debug] [Error] ${msg}\n`;
  const file = process.env['BASH_COMP_DEBUG_FILE'];
  if (file) {
    try { appendFileSync(file, line); } catch { /* best effort */ }
  }
  io.err(line);
}

// ---------------------------------------------------------------------------
// Engine: cobra's getCompletions over a commander tree
// ---------------------------------------------------------------------------

/** A flag as completion sees it. */
interface Flag {
  owner: Command;
  /** Long name without `--`. */
  long?: string;
  /** Shorthand without `-`. */
  short?: string;
  usage: string;
  takesValue: boolean;
  multi: boolean;
  hidden: boolean;
  required: boolean;
  /** Help or version: given, completion stops. */
  stops: boolean;
  inherited: boolean;
  choices?: readonly string[];
}

function flagName(f: Flag): string {
  return f.long ?? f.short ?? '';
}

function toFlag(o: Option, owner: Command, root: Command, inherited: boolean): Flag {
  const help = internals(owner)._getHelpOption?.() === o;
  const version = !help && internals(owner)._versionOptionName === o.attributeName();
  let usage = o.description;
  if (help) usage = `help for ${owner.name()}`;
  else if (version) usage = `version for ${root.name()}`;
  else if (o.argChoices?.length) {
    const suffix = `(one of: ${o.argChoices.join(', ')})`;
    usage = usage ? `${usage} ${suffix}` : suffix;
  }
  return {
    owner,
    long: o.long?.replace(/^--/, ''),
    short: o.short?.replace(/^-/, ''),
    usage,
    takesValue: o.required || o.optional,
    multi: o.variadic,
    hidden: o.hidden,
    required: o.mandatory,
    stops: help || version,
    inherited,
    choices: o.argChoices,
  };
}

/**
 * The flags of the last command of `chain`, as commander accepts them:
 * ancestors' options (unless an ancestor has positional options), then
 * its own plus its help option (a root's version flag stays local to it).
 * A root-alias shim takes no flags of its own, only help, plus the
 * root's persistent flags like any child.
 */
function flagsOf(root: Command, chain: Command[]): Flag[] {
  const cmd = chain[chain.length - 1]!;
  const out: Flag[] = [];
  for (let i = chain.length - 2; i >= 0; i--) {
    const anc = chain[i]!;
    if (internals(anc)._enablePositionalOptions) break;
    // cobra's version flag is local to the root: never inherited.
    for (const o of anc.options) {
      if (internals(anc)._versionOptionName === o.attributeName()) continue;
      out.push(toFlag(o, anc, root, true));
    }
  }
  if (isShim(root, cmd)) {
    out.push({
      owner: cmd, long: 'help', short: 'h', usage: `help for ${cmd.name()}`,
      takesValue: false, multi: false, hidden: false, required: false,
      stops: true, inherited: false,
    });
    return out;
  }
  for (const o of cmd.options) out.push(toFlag(o, cmd, root, false));
  const help = internals(cmd)._getHelpOption?.();
  if (help && !cmd.options.includes(help)) out.push(toFlag(help, cmd, root, false));
  return out;
}

function rootAliases(root: Command): Map<string, string> {
  return (root as Tagged)[ROOT_ALIASES] ?? new Map();
}

/** A hidden root subcommand standing in for a deep root alias. */
function isShim(root: Command, cmd: Command): boolean {
  return cmd.parent === root && rootAliases(root).has(cmd.name());
}

const lookupLong = (flags: Flag[], name: string) => flags.find(f => f.long === name);
const lookupShort = (flags: Flag[], s: string) => flags.find(f => f.short === s);

function subcommand(cmd: Command, word: string): Command | undefined {
  return cmd.commands.find(c => c.name() === word || c.aliases().includes(word));
}

/** pflag `isFlagArg`. */
function isFlagArg(a: string): boolean {
  return (a.length >= 3 && a.startsWith('--')) || (a.length >= 2 && a[0] === '-' && a[1] !== '-');
}

/** Whether a dash word consumes the next one (unknown flags do, as in cobra). */
function consumesNext(flags: Flag[], s: string): boolean {
  if (s.includes('=')) return false;
  if (s.startsWith('--')) return lookupLong(flags, s.slice(2))?.takesValue ?? true;
  if (s.startsWith('-') && s.length === 2) return lookupShort(flags, s.slice(1))?.takesValue ?? true;
  return false;
}

/** cobra `stripFlags`: the non-flag words, skipping flag values. */
function stripFlags(args: string[], flags: Flag[]): string[] {
  const commands: string[] = [];
  for (let i = 0; i < args.length;) {
    const s = args[i++]!;
    if (s === '--') break;
    if (s.startsWith('-') && consumesNext(flags, s)) {
      if (args.length - i <= 1) break;
      i++;
      continue;
    }
    if (s !== '' && !s.startsWith('-')) commands.push(s);
  }
  return commands;
}

/** cobra `argsMinusFirstX`: `args` without the first non-flag `x`. */
function argsMinusFirst(args: string[], x: string, flags: Flag[]): string[] {
  for (let pos = 0; pos < args.length;) {
    const s = args[pos]!;
    if (s === '--') break;
    if (s.startsWith('-') && consumesNext(flags, s)) {
      pos += 2;
      continue;
    }
    if (!s.startsWith('-') && s === x) return [...args.slice(0, pos), ...args.slice(pos + 1)];
    pos++;
  }
  return args;
}

/** cobra `Find`: descend through subcommand names and aliases. */
function find(root: Command, args: string[]) {
  const chain = [root];
  const words: string[] = [];
  let rest = args;
  for (;;) {
    const cur = chain[chain.length - 1]!;
    const flags = flagsOf(root, chain);
    const next = stripFlags(rest, flags)[0];
    if (next === undefined) break;
    const sub = subcommand(cur, next);
    if (!sub) break;
    rest = argsMinusFirst(rest, next, flags);
    words.push(sub.name());
    chain.push(sub);
  }
  return { chain, words, rest };
}

/** pflag's parse of the final command's arguments, with its error texts. */
function parseFlags(flags: Flag[], args: string[]): { seen: Flag[]; positionals: string[] } {
  const seen: Flag[] = [];
  const positionals: string[] = [];
  let dashdash = false;
  for (let i = 0; i < args.length;) {
    const s = args[i++]!;
    if (dashdash || s.length < 2 || !s.startsWith('-')) {
      positionals.push(s);
      continue;
    }
    if (s === '--') {
      dashdash = true;
      continue;
    }
    if (s.startsWith('--')) {
      const body = s.slice(2);
      if (body.startsWith('-') || body.startsWith('=')) throw new Error(`bad flag syntax: ${s}`);
      const eq = body.indexOf('=');
      const name = eq < 0 ? body : body.slice(0, eq);
      const f = lookupLong(flags, name);
      if (!f) throw new Error(`unknown flag: --${name}`);
      if (eq < 0 && f.takesValue) {
        if (i >= args.length) throw new Error(`flag needs an argument: ${s}`);
        i++;
      }
      seen.push(f);
      continue;
    }
    const shorthands = s.slice(1);
    for (let j = 0; j < shorthands.length; j++) {
      const c = shorthands[j]!;
      const f = lookupShort(flags, c);
      if (!f) throw new Error(`unknown shorthand flag: '${c}' in -${shorthands}`);
      seen.push(f);
      const rest = shorthands.slice(j + 1);
      if (rest.startsWith('=')) break;
      if (!f.takesValue) continue;
      if (rest === '') {
        if (i >= args.length) throw new Error(`flag needs an argument: '${c}' in -${shorthands}`);
        i++;
      }
      break;
    }
  }
  return { seen, positionals };
}

interface FlagCheck {
  flag?: Flag;
  args: string[];
  toComplete: string;
  error?: string;
}

/** cobra `checkIfFlagCompletion`: is the word the value of a flag? */
function checkFlagCompletion(cmd: Command, flags: Flag[], args: string[], last: string): FlagCheck {
  let name = '';
  let trimmed = args;
  let withEqual = false;
  let value = last;
  if (last.startsWith('-')) {
    const idx = last.indexOf('=');
    if (idx < 0) return { args, toComplete: last };
    // The last shorthand of a cluster: `-asd=` completes `d`.
    name = last.startsWith('--') ? last.slice(2, idx) : last.slice(0, idx).slice(-1);
    value = last.slice(idx + 1);
    withEqual = true;
  }
  if (name === '' && args.length > 0) {
    const prev = args[args.length - 1]!;
    if (isFlagArg(prev) && !prev.includes('=')) {
      name = prev.startsWith('--') ? prev.slice(2) : prev.slice(-1);
      trimmed = args.slice(0, -1);
    }
  }
  if (name === '') return { args: trimmed, toComplete: value };
  // cobra `findFlag`: a one-letter name is a shorthand first.
  const flag = (name.length === 1 ? lookupShort(flags, name) : undefined) ?? lookupLong(flags, name);
  if (!flag) {
    return {
      args, toComplete: last,
      error: `Subcommand '${cmd.name()}' does not support flag '${name}'`,
    };
  }
  // A boolean flag takes no value: complete the word as an argument.
  if (!withEqual && !flag.takesValue) return { args, toComplete: value };
  return { flag, args: trimmed, toComplete: value };
}

/** Inherited first, then own, each sorted by name (pflag's visit order). */
function orderedFlags(flags: Flag[]): Flag[] {
  const byName = (a: Flag, b: Flag) => {
    const x = flagName(a);
    const y = flagName(b);
    return x < y ? -1 : x > y ? 1 : 0;
  };
  return [
    ...flags.filter(f => f.inherited).sort(byName),
    ...flags.filter(f => !f.inherited).sort(byName),
  ];
}

/** `--long<TAB>usage` and `-s<TAB>usage` when they match; nothing for a hidden flag. */
function flagNameCompletions(f: Flag, toComplete: string): string[] {
  if (f.hidden) return [];
  const out: string[] = [];
  if (f.long && `--${f.long}`.startsWith(toComplete)) out.push(`--${f.long}\t${f.usage}`);
  if (f.short && `-${f.short}`.startsWith(toComplete)) out.push(`-${f.short}\t${f.usage}`);
  return out;
}

function requiredFlags(flags: Flag[], seen: Flag[], toComplete: string): string[] {
  return orderedFlags(flags)
    .filter(f => f.required && !seen.includes(f))
    .flatMap(f => flagNameCompletions(f, toComplete));
}

/** The choices of the positional at index `n`, the variadic last one covering the rest. */
function positionalChoices(cmd: Command, n: number): readonly string[] | undefined {
  const args = cmd.registeredArguments;
  const last = args[args.length - 1];
  const a = args[n] ?? (last?.variadic ? last : undefined);
  return a?.argChoices;
}

interface Outcome {
  completions: string[];
  directive: number;
  error?: string;
}

async function getCompletions(root: Command, args: string[]): Promise<Outcome> {
  const toCompleteRaw = args.length ? args[args.length - 1]! : '';
  const { chain, words, rest } = find(root, args.slice(0, -1));
  const cmd = chain[chain.length - 1]!;
  const flags = flagsOf(root, chain);

  const fc = checkFlagCompletion(cmd, flags, rest, toCompleteRaw);
  const { flag, toComplete } = fc;
  // A `--` already on the line turns later dash words into arguments.
  const flagCompletion = !fc.args.includes('--');
  let parsed;
  try {
    parsed = parseFlags(flags, fc.args);
  } catch (e) {
    return {
      completions: [], directive: ShellCompDirective.Default,
      error: `Error while parsing flags from args [${fc.args.join(' ')}]: ${(e as Error).message}`,
    };
  }
  if (fc.error && flagCompletion) {
    return { completions: [], directive: ShellCompDirective.Default, error: fc.error };
  }
  if (parsed.seen.some(f => f.stops)) {
    return { completions: [], directive: ShellCompDirective.NoFileComp };
  }
  const finalArgs = parsed.positionals;
  const ctx: CompletionContext = { commandPath: words.join(' '), args: finalArgs };

  let completions: string[] = [];
  if (!flag && toComplete.startsWith('-') && !toComplete.includes('=') && flagCompletion) {
    completions = requiredFlags(flags, parsed.seen, toComplete);
    if (completions.length === 0) {
      for (const f of orderedFlags(flags)) {
        if (!parsed.seen.includes(f) || f.multi) completions.push(...flagNameCompletions(f, toComplete));
      }
    }
    const noSpace = completions.length === 1 && completions[0]!.endsWith('=');
    return {
      completions,
      directive: noSpace ? ShellCompDirective.NoSpace : ShellCompDirective.NoFileComp,
    };
  }

  let directive: number = ShellCompDirective.Default;
  if (!flag) {
    const localGiven = parsed.seen.some(f => !f.inherited);
    if (finalArgs.length === 0 && !localGiven) {
      const subs = cmd.commands
        .filter(s => !internals(s)._hidden && !(s as Tagged)[BUILTIN])
        .sort((a, b) => (a.name() < b.name() ? -1 : a.name() > b.name() ? 1 : 0));
      for (const s of subs) {
        if (s.name().startsWith(toComplete)) {
          completions.push(`${s.name()}\t${s.summary() || s.description()}`);
        }
        directive = ShellCompDirective.NoFileComp;
      }
    }
    completions.push(...requiredFlags(flags, parsed.seen, toComplete));

    // Positional choices play cobra's ValidArgs: they win over a binding.
    const choices = positionalChoices(cmd, finalArgs.length);
    if (choices?.length) {
      completions.push(...choices.filter(v => v.startsWith(toComplete)));
      return { completions, directive: ShellCompDirective.NoFileComp };
    }
  }

  const result = flag && flagCompletion
    ? await flagValueCompletion(flag, ctx, toComplete)
    : await argsCompletion(root, cmd, ctx, toComplete);
  if (result) {
    completions.push(...result.completions);
    directive = result.directive;
  }
  return { completions, directive };
}

/** The completer bound to `flag`, else its choices; null when neither exists. */
async function flagValueCompletion(flag: Flag, ctx: CompletionContext, toComplete: string) {
  const bound = (flag.owner as Tagged)[FLAG_COMPLETERS]?.get(flagName(flag));
  if (bound) return resolveCompletions(bound, toComplete, ctx);
  if (!flag.choices?.length) return null;
  const lower = toComplete.toLowerCase();
  return {
    completions: flag.choices.filter(v => v.toLowerCase().startsWith(lower)),
    directive: ShellCompDirective.NoFileComp,
  };
}

/** The completer bound to `cmd`'s positionals, plus root aliases at the root. */
async function argsCompletion(root: Command, cmd: Command, ctx: CompletionContext, toComplete: string) {
  const bound = (cmd as Tagged)[ARGS_COMPLETER];
  const aliases = cmd === root ? rootAliases(root) : new Map<string, string>();
  if (!bound && aliases.size === 0) return null;
  const r = bound
    ? await resolveCompletions(bound, toComplete, ctx)
    : { completions: [] as string[], directive: ShellCompDirective.Default as number };
  if (aliases.size > 0) {
    r.directive |= ShellCompDirective.NoFileComp;
    for (const [name, target] of [...aliases].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))) {
      if (name.startsWith(toComplete)) r.completions.push(`${name}\talias for ${target}`);
    }
  }
  return r;
}
