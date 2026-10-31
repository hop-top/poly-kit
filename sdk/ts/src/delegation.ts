/**
 * @module delegation
 * @package @hop-top/kit
 *
 * The delegation gates a leaf passes before its handler runs, in Go's
 * order (`policy_runE.go`, `global_dryrun.go`): `--dry-run` enforcement,
 * the named `--policy`, the `--confirm` matrix with the typed
 * `--confirm-token`, and the `--max-ops` budget accounted after a
 * successful mutating run.
 *
 * `createCLI` installs the root flags and the hooks; adopters declare
 * `kit/side-effect` on a leaf and the gates follow.
 */

import { createHash } from 'node:crypto';
import * as fs from 'node:fs';
import { Command, Option } from 'commander';

import {
  type CliError,
  EXIT_USAGE,
  rateLimitedError,
  renderError,
  TRANSIENCE_TRANSIENT,
  unauthorizedError,
  usageError,
  withTransience,
} from './output/error.js';
import { renderPlan } from './plan.js';
import {
  Engine,
  emptyPolicy,
  KIT_DEFAULT_NAME,
  kitDefault,
  MaxOpsExceededError,
  type Policy,
  type PolicyLoader,
} from './policy.js';
import {
  gateSideEffect,
  isDestructiveLike,
  inferredDestructive,
  isDestructiveName,
  isValidSideEffect,
  isWriteLike,
  SIDE_EFFECT_ANNOTATION,
} from './sideeffect.js';

/** `kit/destructive-token: required` demands `--confirm-token=<sha>` too. */
export const DESTRUCTIVE_TOKEN_ANNOTATION = 'kit/destructive-token';
/** `kit/dry-run`: `supported` (legacy) or `opted-out`. */
export const DRY_RUN_ANNOTATION = 'kit/dry-run';
/** Why a command opted out of `--dry-run`. */
export const DRY_RUN_RATIONALE_ANNOTATION = 'kit/dry-run-rationale';

const CONFIRM_TOKEN_FLAG = '--confirm-token';
const SUPERVISOR_COMMAND = 'serve';
const BUILTIN_NAMES = new Set(['help', 'completion', '__complete', '__completeNoDesc']);

type Annotations = Readonly<Record<string, string>>;

// ---------------------------------------------------------------------------
// Prompt source
// ---------------------------------------------------------------------------

/** The terminal an interactive prompt asks on. */
export interface Prompter {
  /**
   * Writes `question` and reads one line of answer, lowercased and
   * trimmed; `null` on end of input.
   */
  ask(question: string): string | null;
}

/** Supplies the prompt terminal: `null` means nobody to ask. */
export type PromptSource = () => Prompter | null;

/** No terminal: every prompt is refused and an unset `--confirm` is `no`. */
export const noPrompt: PromptSource = () => null;

/** A scripted terminal answering from `answers`, one per question. */
export function scriptedPrompt(answers: readonly string[]): PromptSource {
  const queue = [...answers];
  const p: Prompter = {
    ask() {
      const a = queue.shift();
      return a === undefined ? null : a.trim().toLowerCase();
    },
  };
  return () => p;
}

let ttyPrompter: Prompter | null | undefined;

/**
 * The process's controlling terminal (`/dev/tty`), opened once; none
 * where there is no such terminal. The default. Both halves go to the
 * terminal, never to stdin or stderr: reading stdin would consume the
 * command's payload, and writing a redirected stderr would put the
 * question where the operator cannot see it.
 */
export const controllingTTY: PromptSource = () => {
  if (ttyPrompter !== undefined) return ttyPrompter;
  try {
    const fd = fs.openSync('/dev/tty', 'r+');
    ttyPrompter = {
      ask(question) {
        fs.writeSync(fd, question);
        const buf = Buffer.alloc(1);
        let line = '';
        for (;;) {
          let n = 0;
          try {
            n = fs.readSync(fd, buf, 0, 1, null);
          } catch {
            n = 0;
          }
          if (n === 0) return line === '' ? null : line.trim().toLowerCase();
          const ch = buf.toString('utf8');
          if (ch === '\n') return line.trim().toLowerCase();
          line += ch;
        }
      },
    };
  } catch {
    ttyPrompter = null;
  }
  return ttyPrompter;
};

// ---------------------------------------------------------------------------
// Confirm mode
// ---------------------------------------------------------------------------

export type ConfirmMode = 'auto' | 'yes' | 'no' | 'prompt';

/**
 * Parses `--confirm`: empty or unknown defaults to `prompt` when there is
 * a terminal to ask on and `no` otherwise. Interactivity is the
 * terminal's availability, not isatty on stdin.
 */
export function resolveConfirmMode(src: PromptSource, raw: string): ConfirmMode {
  const v = raw.trim().toLowerCase();
  if (v === 'yes' || v === 'no' || v === 'auto' || v === 'prompt') return v;
  return src() !== null ? 'prompt' : 'no';
}

/**
 * The token a typed-confirmation command demands: the first 12 hex
 * characters of the sha256 of its full command path.
 */
export function destructiveToken(commandPath: string): string {
  return createHash('sha256').update(commandPath).digest('hex').slice(0, 12);
}

function requiresToken(ann: Annotations): boolean {
  const v = ann[DESTRUCTIVE_TOKEN_ANNOTATION];
  return v === 'required' || v === 'true';
}

// ---------------------------------------------------------------------------
// Dry-run policy
// ---------------------------------------------------------------------------

/** What `--dry-run` does on a leaf, by its annotations. */
export type DryRunPolicy =
  | 'allow'
  | 'noop'
  | 'reject-interactive'
  | 'reject-opt-out'
  | 'reject-untagged'
  | 'reject-malformed';

/**
 * The dry-run table: opted-out refuses; legacy `supported` allows; write
 * and destructive tiers allow; read is a silent no-op; interactive
 * refuses; malformed values refuse. An unannotated command with a
 * destructive name is the inferred destructive tier, so it previews like a
 * declared one; any other untagged command refuses.
 */
export function resolveDryRunPolicy(name: string, ann: Annotations): DryRunPolicy {
  switch (ann[DRY_RUN_ANNOTATION]) {
    case 'opted-out':
      return 'reject-opt-out';
    case 'supported':
      return 'allow';
  }
  const s = ann[SIDE_EFFECT_ANNOTATION];
  if (s === undefined || s === '') {
    return isDestructiveName(name) ? 'allow' : 'reject-untagged';
  }
  if (isWriteLike(s) || isDestructiveLike(s)) return 'allow';
  if (s === 'read') return 'noop';
  if (s === 'interactive') return 'reject-interactive';
  return 'reject-malformed';
}

function dryRunRefusal(path: string, ann: Annotations, p: DryRunPolicy): CliError | null {
  let msg: string;
  let fix: string;
  switch (p) {
    case 'reject-interactive':
      msg =
        `${JSON.stringify(path)} does not accept --dry-run: interactive ` +
        'sessions have no batch boundary to scope the preview';
      fix = 'run without --dry-run';
      break;
    case 'reject-opt-out':
      msg =
        `${JSON.stringify(path)} does not support --dry-run: the command ` +
        'explicitly opted out via cli.OptOutDryRun';
      if (ann[DRY_RUN_RATIONALE_ANNOTATION]) msg += ` (${ann[DRY_RUN_RATIONALE_ANNOTATION]})`;
      fix = "run without --dry-run; see the command's documentation for the rationale";
      break;
    case 'reject-untagged':
      msg =
        `${JSON.stringify(path)} cannot apply --dry-run: the command is ` +
        'missing the required kit/side-effect tag';
      fix = 'adopter must declare the tier with the kit/side-effect annotation';
      break;
    case 'reject-malformed':
      msg =
        `${JSON.stringify(path)} cannot apply --dry-run: the command has a ` +
        `malformed kit/side-effect tag ${JSON.stringify(ann[SIDE_EFFECT_ANNOTATION] ?? '')} ` +
        'that names no tier';
      fix = 'adopter must declare one of the kit side-effect tiers';
      break;
    default:
      return null;
  }
  return { ...usageError(msg), suggested_fix: fix };
}

// ---------------------------------------------------------------------------
// Installation
// ---------------------------------------------------------------------------

/** Options {@link installDelegation} takes; `createCLI` forwards its own. */
export interface DelegationOptions {
  /**
   * Resolves a named `--policy`. Undefined wires none: only the reserved
   * `kit-default` resolves, and any other name is a usage error.
   */
  policy?: PolicyLoader;
  /** The terminal prompts use; defaults to the controlling terminal. */
  promptSource?: PromptSource;
  /** Drop `--dry-run` (it is then never applied). */
  disableDryRun?: boolean;
}

interface Run {
  engine: Engine;
  dryRun: boolean;
  tier: string | undefined;
}

interface State {
  opts: DelegationOptions;
  /** Gate flags read from before a shadowing command, for this parse. */
  shadow: Map<string, string | boolean>;
  runs: WeakMap<Command, Run>;
}

const states = new WeakMap<Command, State>();

const GATE_FLAGS: ReadonlyArray<{ long: string; value: boolean }> = [
  { long: '--confirm', value: true },
  { long: '--max-ops', value: true },
  { long: '--policy', value: true },
  { long: '--dry-run', value: false },
];

function annotationsOf(cmd: Command): Annotations {
  return (cmd as { annotations?: Record<string, string> }).annotations ?? {};
}

function isRunnable(cmd: Command): boolean {
  return (cmd as unknown as { _actionHandler?: unknown })._actionHandler != null;
}

function isBuiltin(cmd: Command): boolean {
  return BUILTIN_NAMES.has(cmd.name()) || cmd.parent?.name() === 'completion';
}

function camel(long: string): string {
  return long.replace(/^--/, '').replace(/-([a-z])/g, (_, c: string) => c.toUpperCase());
}

/** `tool a b`: the root name and the command path. */
function commandPath(cmd: Command): string {
  const names: string[] = [];
  for (let c: Command | null = cmd; c; c = c.parent) names.unshift(c.name());
  return names.join(' ');
}

/** `a b`: the path below the root, which policy globs match. */
function verbOf(cmd: Command): string {
  const names: string[] = [];
  for (let c: Command | null = cmd; c?.parent; c = c.parent) names.unshift(c.name());
  return names.join(' ');
}

function rootOf(cmd: Command): Command {
  let c = cmd;
  while (c.parent) c = c.parent;
  return c;
}

function declares(cmd: Command, long: string): boolean {
  return cmd.options.some((o) => o.long === long);
}

/**
 * Installs the delegation gates on `program`: the root flags `--dry-run`,
 * `--confirm`, `--max-ops` and `--policy` (hidden, as in Go), the
 * pre-action gate and the post-action ops accounting, and the parse
 * wrappers that register `--confirm-token` on `kit/destructive-token`
 * leaves and keep a leaf-local flag of a gate flag's name from being read
 * as the gate.
 */
export function installDelegation(program: Command, opts: DelegationOptions = {}): void {
  if (states.has(program)) return;
  const st: State = { opts, shadow: new Map(), runs: new WeakMap() };
  states.set(program, st);

  const hidden = (o: Option): Option => o.hideHelp();
  if (!opts.disableDryRun) {
    program.addOption(
      hidden(new Option('--dry-run', 'Preview side effects without applying them (must be supported by the command).')),
    );
  }
  program.addOption(
    hidden(
      new Option(
        '--confirm <mode>',
        'Confirmation policy for destructive commands (auto|yes|no|prompt). Default: prompt on a TTY, no otherwise.',
      ),
    ),
  );
  program.addOption(
    hidden(
      new Option('--max-ops <n>', 'Cap mutating operations per invocation. 0 = unlimited.').argParser(
        (v: string): number => {
          if (!/^[-+]?\d+$/.test(v)) {
            program.error(`error: option '--max-ops <n>' argument '${v}' is invalid. expected an integer`, {
              exitCode: EXIT_USAGE,
              code: 'commander.invalidArgument',
            });
          }
          return Number.parseInt(v, 10);
        },
      ),
    ),
  );
  program.addOption(
    hidden(
      new Option(
        '--policy <name>',
        'Named delegation policy (loaded from $XDG_CONFIG_HOME/<tool>/policies/<name>.yaml).',
      ),
    ),
  );

  program.hook('preAction', (_this, action) => {
    if (isBuiltin(action)) return;
    gate(program, st, action);
  });
  program.hook('postAction', (_this, action) => {
    const run = st.runs.get(action);
    if (!run || run.dryRun) return;
    if (run.tier === undefined || !(isWriteLike(run.tier) || isDestructiveLike(run.tier))) return;
    try {
      run.engine.recordOp();
    } catch (e) {
      if (e instanceof MaxOpsExceededError) {
        refuse(program, rateLimitedError(`max-ops budget exceeded after running ${commandPath(action)}`));
      }
      throw e;
    }
  });

  wrapParse(program, st);
}

/** True when kit applied `--dry-run` to the running leaf (it previews). */
export function dryRunApplied(cmd: Command): boolean {
  const st = states.get(rootOf(cmd));
  return st?.runs.get(cmd)?.dryRun === true;
}

/**
 * True when the command runs as a dry run: kit applied `--dry-run`, or the
 * flag was given (a read command accepts it and still runs). Handlers call
 * this and build a {@link Plan} instead of acting.
 */
export function isDryRun(cmd: Command): boolean {
  if (dryRunApplied(cmd)) return true;
  const root = rootOf(cmd);
  const st = states.get(root);
  return st?.shadow.get('--dry-run') === true || root.getOptionValue('dryRun') === true;
}

// ---------------------------------------------------------------------------
// Flag reads
// ---------------------------------------------------------------------------

/** The root's value of gate flag `long`, honoring flags read before a shadowing command. */
function rootFlag(program: Command, st: State, long: string): unknown {
  return st.shadow.has(long) ? st.shadow.get(long) : program.getOptionValue(camel(long));
}

/**
 * A persistent flag's string value visible to `cmd`: the nearest command
 * declaring it wins, as Go's flagValue walks up the parent chain.
 */
function flagValue(program: Command, st: State, cmd: Command, long: string): string {
  for (let c: Command | null = cmd; c; c = c.parent) {
    if (c === program) return stringOf(rootFlag(program, st, long));
    if (declares(c, long)) return stringOf(c.getOptionValue(camel(long)));
  }
  return '';
}

function stringOf(v: unknown): string {
  return v === undefined || v === null ? '' : String(v);
}

function flagInt(program: Command, st: State, cmd: Command, long: string): number {
  const m = /^[-+]?\d+/.exec(flagValue(program, st, cmd, long));
  return m ? Number.parseInt(m[0], 10) : 0;
}

// ---------------------------------------------------------------------------
// The gate
// ---------------------------------------------------------------------------

function buildEngine(st: State, name: string, maxOps: number): Engine {
  let policy: Policy;
  if (name === KIT_DEFAULT_NAME) {
    policy = kitDefault();
  } else if (name !== '') {
    if (!st.opts.policy) {
      throw usageError('--policy is set but no policy loader is wired (createCLI policy option)');
    }
    try {
      policy = st.opts.policy(name);
    } catch (e) {
      // A policy that cannot be read or parsed is a usage failure, not an
      // authorization refusal: the gate never ran.
      throw usageError(`policy ${JSON.stringify(name)}: ${(e as Error).message}`);
    }
  } else {
    policy = emptyPolicy();
  }
  return new Engine(policy, maxOps);
}

function startsServices(program: Command, cmd: Command): boolean {
  return cmd.parent === program && cmd.name() === SUPERVISOR_COMMAND;
}

function gate(program: Command, st: State, cmd: Command): void {
  const ann = annotationsOf(cmd);
  const tier = gateSideEffect(cmd.name(), ann);
  const path = commandPath(cmd);

  try {
    // Fail closed, as Go's Root.Validate does: a declared tier outside the
    // closed set would match no class and run unconfirmed.
    const declared = ann[SIDE_EFFECT_ANNOTATION];
    if (declared !== undefined && declared !== '' && !isValidSideEffect(declared)) {
      throw {
        ...usageError(
          `${JSON.stringify(path)} has a malformed kit/side-effect tag ${JSON.stringify(declared)} that names no tier`,
        ),
        suggested_fix: 'declare one of the kit side-effect tiers',
      };
    }

    let dryRun = false;
    if (!st.opts.disableDryRun && rootFlag(program, st, '--dry-run') === true) {
      const p = resolveDryRunPolicy(cmd.name(), ann);
      const refusal = dryRunRefusal(path, ann, p);
      if (refusal) throw refusal;
      dryRun = p === 'allow';
    }

    // --policy is read from the root only: a command's own --policy is
    // not a delegation policy name.
    const policyName = stringOf(rootFlag(program, st, '--policy'));
    const engine = buildEngine(st, policyName, flagInt(program, st, cmd, '--max-ops'));
    st.runs.set(cmd, { engine, dryRun, tier });

    // Policy first: a refusal here is final and --confirm=yes does not lift it.
    let policyConfirm = false;
    if (!startsServices(program, cmd)) {
      const v = engine.authorize({ verb: verbOf(cmd), sideEffect: tier });
      if (!v.allowed) throw unauthorizedError(v.reason);
      policyConfirm = v.requireConfirm;
    }

    // An inferred destructive leaf never promised to honor --dry-run, so its
    // handler may really mutate: kit renders the preview itself and never
    // invokes the handler. Authorized above, so a policy refusal still wins.
    if (dryRun && inferredDestructive(cmd.name(), ann) && ann[DRY_RUN_ANNOTATION] === undefined) {
      previewInferred(program, cmd, path);
    }

    // Skipped only for a dry run kit applied: the bare flag is not enough,
    // since on a leaf that does not honor it the command would act unconfirmed.
    if (!dryRun && ((tier !== undefined && isDestructiveLike(tier)) || policyConfirm)) {
      gateConfirm(program, st, cmd, tier ?? '', path, ann);
    }
  } catch (e) {
    if (isEnvelope(e)) refuse(program, e);
    throw e;
  }
}

function previewInferred(program: Command, cmd: Command, path: string): never {
  const chunks: string[] = [];
  renderPlan(
    { write: (c: string) => chunks.push(c) },
    activeFormat(program),
    {
      command: path,
      effects: [{ kind: cmd.name(), target: path, reversible: false, detail: 'destructive by name; handler not run' }],
      warnings: ['kit/side-effect is not declared: the handler was not invoked'],
    },
    path,
  );
  program.configureOutput().writeOut?.(chunks.join(''));
  return (program as unknown as { _exit(code: number, name: string, msg: string): never })._exit(
    0,
    'kit.dryRunPreview',
    'dry-run preview',
  );
}

function isEnvelope(e: unknown): e is CliError {
  return typeof e === 'object' && e !== null && 'code' in e && 'exit_code' in e;
}

function gateConfirm(
  program: Command,
  st: State,
  cmd: Command,
  tier: string,
  path: string,
  ann: Annotations,
): void {
  const src = st.opts.promptSource ?? controllingTTY;
  const mode = resolveConfirmMode(src, flagValue(program, st, cmd, '--confirm'));

  // Typed-token destructives never let --confirm=yes alone proceed.
  if (requiresToken(ann)) {
    const expected = destructiveToken(path);
    const got = stringOf(cmd.getOptionValue('confirmToken'));
    if (got === '') {
      const refusal = unauthorizedError(`destructive command ${path} requires ${CONFIRM_TOKEN_FLAG}=${expected}`);
      if (isMachineFormat(program)) {
        // The preview would leave stderr unparseable; the envelope carries
        // the instruction instead.
        refusal.suggested_fix = `re-run with ${CONFIRM_TOKEN_FLAG}=${expected} to proceed`;
      } else {
        writeErr(
          program,
          'This is a destructive operation requiring typed confirmation.\n' +
            `Token: ${expected}\nRe-run with ${CONFIRM_TOKEN_FLAG}=${expected} to proceed.\n`,
        );
      }
      throw refusal;
    }
    if (got !== expected) {
      throw unauthorizedError(`${CONFIRM_TOKEN_FLAG} mismatch for ${path}; expected ${expected}`);
    }
    return;
  }

  switch (mode) {
    case 'yes':
    case 'auto':
      return;
    case 'no':
      // Transient: the caller clears it by re-invoking with --confirm=yes.
      throw withTransience(
        unauthorizedError(`destructive command ${path} refused: --confirm=no (or non-TTY default)`),
        TRANSIENCE_TRANSIENT,
      );
    case 'prompt': {
      const answer = src()?.ask(`This is a ${tier} operation (${path}). Continue? [y/N] `) ?? null;
      if (answer === 'y' || answer === 'yes') return;
      throw withTransience(
        unauthorizedError(`aborted by user at confirm prompt for ${path}`),
        TRANSIENCE_TRANSIENT,
      );
    }
  }
}

// ---------------------------------------------------------------------------
// Refusal output
// ---------------------------------------------------------------------------

function activeFormat(program: Command): string {
  return stringOf(program.getOptionValue('format'));
}

function isMachineFormat(program: Command): boolean {
  const f = activeFormat(program);
  return f === 'json' || f === 'yaml';
}

function writeErr(program: Command, s: string): void {
  program.configureOutput().writeErr?.(s);
}

/**
 * Renders `e` once on stderr in the active `--format` and exits with its
 * code, through commander's exit path so `exitOverride` still applies.
 */
function refuse(program: Command, e: CliError): never {
  const chunks: string[] = [];
  renderError({ write: (c: string) => chunks.push(c) }, activeFormat(program), e);
  writeErr(program, chunks.join(''));
  return (program as unknown as { _exit(code: number, name: string, msg: string): never })._exit(
    e.exit_code,
    'kit.delegation',
    e.message,
  );
}

// ---------------------------------------------------------------------------
// Parse wrappers
// ---------------------------------------------------------------------------

function walkAll(cmd: Command, visit: (c: Command) => void): void {
  visit(cmd);
  for (const c of cmd.commands as Command[]) walkAll(c, visit);
}

/**
 * Registers `--confirm-token` on every runnable leaf opting in via the
 * `kit/destructive-token` annotation. Idempotent.
 */
export function installConfirmTokenFlag(program: Command): void {
  walkAll(program, (c) => {
    if (c === program || c.commands.length > 0 || !isRunnable(c) || isBuiltin(c)) return;
    if (!requiresToken(annotationsOf(c)) || declares(c, CONFIRM_TOKEN_FLAG)) return;
    c.option(
      `${CONFIRM_TOKEN_FLAG} <token>`,
      "Typed confirmation token (sha) printed by the command's prompt. Required for kit/destructive-token commands.",
    );
  });
}

type ParseFrom = 'node' | 'electron' | 'user';

interface Scan {
  user: string[];
  /** Gate flags whose root option is withdrawn for this parse. */
  shadowed: string[];
}

/** The option `tok` names on `cur` or the root, for arity decisions. */
function findOption(program: Command, cur: Command, tok: string): Option | undefined {
  const long = tok.startsWith('--') ? tok.split('=')[0]! : undefined;
  const match = (o: Option): boolean => (long ? o.long === long : o.short === tok);
  return cur.options.find(match) ?? program.options.find(match);
}

/**
 * Reads the gate flags a command-line gives the root, leaving a flag a
 * selected command declares itself to that command. Commander hands every
 * known root option to the root wherever it appears; Go's persistent flags
 * yield to a command's own flag of the same name, and so must this.
 */
function scan(program: Command, st: State, user: string[]): Scan {
  const conflicts = new Set<string>();
  walkAll(program, (c) => {
    if (c === program) return;
    for (const g of GATE_FLAGS) if (declares(c, g.long)) conflicts.add(g.long);
  });
  if (conflicts.size === 0) return { user, shadowed: [] };

  // Resolve the selected command path and each command's name position.
  const nameAt: Array<{ cmd: Command; idx: number }> = [];
  let cur = program;
  let resolving = true;
  for (let i = 0; i < user.length && resolving; i++) {
    const tok = user[i]!;
    if (tok === '--') break;
    if (tok.startsWith('-') && tok !== '-') {
      const opt = findOption(program, cur, tok);
      if (opt && !tok.includes('=') && (opt.required || (opt.optional && !(user[i + 1] ?? '-').startsWith('-')))) i++;
      continue;
    }
    const sub = (cur.commands as Command[]).find((c) => c.name() === tok || c.aliases().includes(tok));
    if (sub) {
      cur = sub;
      nameAt.push({ cmd: sub, idx: i });
    } else {
      resolving = false;
    }
  }

  // The shallowest selected command declaring a gate flag owns it from its name on.
  const boundary = new Map<string, number>();
  for (const g of GATE_FLAGS) {
    const owner = nameAt.find((n) => declares(n.cmd, g.long));
    if (owner) boundary.set(g.long, owner.idx);
  }
  if (boundary.size === 0) return { user, shadowed: [] };

  const out: string[] = [];
  for (let i = 0; i < user.length; i++) {
    const tok = user[i]!;
    if (tok === '--') {
      out.push(...user.slice(i));
      break;
    }
    const g = tok.startsWith('--') ? GATE_FLAGS.find((f) => tok === f.long || tok.startsWith(`${f.long}=`)) : undefined;
    const b = g ? boundary.get(g.long) : undefined;
    if (g && b !== undefined && i < b) {
      if (!g.value) {
        st.shadow.set(g.long, true);
      } else if (tok.includes('=')) {
        st.shadow.set(g.long, tok.slice(tok.indexOf('=') + 1));
      } else {
        st.shadow.set(g.long, user[i + 1] ?? '');
        i++;
      }
      continue;
    }
    out.push(tok);
    // Skip the value of any other value-taking option so it is not mistaken for a flag.
    if (tok.startsWith('-') && tok !== '-' && !tok.includes('=')) {
      let c = program;
      for (const n of nameAt) if (n.idx < i) c = n.cmd;
      const opt = findOption(program, c, tok);
      if (opt?.required && i + 1 < user.length) out.push(user[++i]!);
    }
  }
  return { user: out, shadowed: [...boundary.keys()] };
}

function wrapParse(program: Command, st: State): void {
  const origParse = program.parse.bind(program);
  const origParseAsync = program.parseAsync.bind(program);

  /** Runs `go` with the argv rewritten for shadowing, restoring root options after. */
  const prepare = <R>(
    argv: readonly string[] | undefined,
    options: { from?: ParseFrom } | undefined,
    go: (argv: readonly string[] | undefined, options: { from?: ParseFrom } | undefined) => R,
    after: (r: R, restore: () => void) => R,
  ): R => {
    installConfirmTokenFlag(program);
    st.shadow.clear();
    let from: ParseFrom = options?.from ?? 'node';
    let args = argv;
    if (args === undefined) {
      if (process.versions['electron']) from = options?.from ?? 'electron';
      args = process.argv;
    }
    const skip = from === 'user' ? 0 : from === 'electron' && !(process as { defaultApp?: boolean }).defaultApp ? 1 : 2;
    const sc = scan(program, st, args.slice(skip));
    if (sc.shadowed.length === 0) return go(argv, options);

    const removed: Option[] = [];
    for (const long of sc.shadowed) {
      const opts = program.options as Option[];
      const i = opts.findIndex((o) => o.long === long);
      if (i >= 0) removed.push(...opts.splice(i, 1));
    }
    const restore = (): void => {
      (program.options as Option[]).push(...removed);
    };
    let r: R;
    try {
      r = go([...args.slice(0, skip), ...sc.user], { ...options, from });
    } catch (e) {
      restore();
      throw e;
    }
    return after(r, restore);
  };

  program.parse = ((argv?: readonly string[], options?: { from?: ParseFrom }) =>
    prepare(
      argv,
      options,
      (a, o) => origParse(a as string[] | undefined, o as { from: ParseFrom } | undefined),
      (r, restore) => {
        restore();
        return r;
      },
    )) as typeof program.parse;

  program.parseAsync = ((argv?: readonly string[], options?: { from?: ParseFrom }) =>
    prepare(
      argv,
      options,
      (a, o) => origParseAsync(a as string[] | undefined, o as { from: ParseFrom } | undefined),
      (r, restore) => r.finally(restore),
    )) as typeof program.parseAsync;
}
