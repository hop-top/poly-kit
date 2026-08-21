/**
 * @module mcp/bridge
 * @package @hop-top/kit
 *
 * A commander adapter for the MCP surface's `McpBridge` interface.
 *
 * The surface itself never imports commander — that is what keeps
 * the handler framework-free and lets adopters on another command
 * framework implement `McpBridge` directly. This adapter is the
 * convenience path for the commander trees the TS SDK's own `cli`
 * module builds.
 *
 * The safety gate lives here, on the invoke path, so BOTH eras
 * inherit one destructive ceiling from one place: there is no way to
 * reach a leaf through the modern handler that the legacy handler
 * would have blocked.
 */

import type { Command, Option } from 'commander';
import {
  DestructiveBlockedError,
  policyAllowed,
  SurfaceNotEnabledError,
  toolName,
  UnknownCommandError,
  defaultPolicy,
  type FlagSchema,
  type Invocation,
  type InvokeResult,
  type Leaf,
  type McpBridge,
  type Policy,
  type SafetyClass,
  type Surface,
} from './types.js';

/** Annotation keys read off a command, matching the kit/ vocabulary. */
const ANN_SIDE_EFFECT = 'kit/side-effect';
const ANN_AUTH_REQUIRED = 'kit/auth-required';
const ANN_REQUIRES_CONFIRM = 'kit/requires-confirmation';
const ANN_PERMISSIONS = 'kit/permissions';
const ANN_EXIT_CODES = 'kit/exit-codes';

/**
 * A command carrying kit annotations. commander has no annotations
 * concept, so they ride an optional property the CLI builder sets.
 */
export interface AnnotatedCommand extends Command {
  annotations?: Record<string, string>;
}

export interface CommanderBridgeOptions {
  /** Policy gate. Defaults to defaultPolicy() (block remote destructive). */
  policy?: Policy;
  /**
   * Runs one leaf and returns its result. Adopters supply this
   * because kit does not dictate how a command's output is captured.
   */
  run: (inv: Invocation, cmd: Command) => Promise<InvokeResult> | InvokeResult;
  /** Per-leaf surface enablement override. */
  enabled?: (leaf: Leaf) => Partial<Record<Surface, boolean>> | undefined;
}

/**
 * Builds an `McpBridge` over a commander tree. Leaves are the
 * commands with no subcommands, enumerated depth-first in
 * declaration order, with the root's own name excluded from the path
 * (so `root widget add` is the tool `widget.add`).
 */
export function commanderBridge(
  root: Command,
  opts: CommanderBridgeOptions,
): McpBridge {
  const policy = opts.policy ?? defaultPolicy();

  const leaves = (): Leaf[] => {
    const out: Leaf[] = [];
    walkLeaves(root, [], (cmd, path) => {
      const leaf: Leaf = {
        path,
        description: cmd.description() ?? '',
        ...collectFlags(cmd),
        class: classify(cmd as AnnotatedCommand),
      };
      const en = opts.enabled?.(leaf);
      if (en) leaf.enabled = en;
      out.push(leaf);
    });
    return out;
  };

  return {
    leaves,
    async invoke(inv: Invocation): Promise<InvokeResult> {
      const target = leaves().find(
        (l) =>
          l.path.length === inv.path.length &&
          l.path.every((seg, i) => seg === inv.path[i]),
      );
      if (target === undefined) {
        throw new UnknownCommandError(
          `unknown command: ${toolName(inv.path)}`,
        );
      }
      if (target.enabled?.[inv.meta.surface] === false) {
        throw new SurfaceNotEnabledError(
          `surface not enabled: ${inv.meta.surface}`,
        );
      }
      // The destructive ceiling. Empty allowDestructiveOn means
      // block-all, so a destructive leaf is unreachable on MCP unless
      // the adopter explicitly opts that surface in.
      if (!policyAllowed(policy, target.class ?? {}, inv.meta.surface)) {
        throw new DestructiveBlockedError(
          target.path.join(' '),
          inv.meta.surface,
        );
      }
      const cmd = findCommand(root, target.path);
      if (cmd === undefined) {
        throw new UnknownCommandError(
          `unknown command: ${toolName(inv.path)}`,
        );
      }
      return opts.run(inv, cmd);
    },
  };
}

/** Reads a command's kit annotations into a SafetyClass. */
export function classify(cmd: AnnotatedCommand): SafetyClass {
  const ann = cmd.annotations ?? {};
  const cls: SafetyClass = {};
  switch (ann[ANN_SIDE_EFFECT]) {
    case 'destructive':
    case 'destructive-local':
    case 'destructive-shared':
      cls.destructive = true;
      break;
    default:
      break;
  }
  if (ann[ANN_AUTH_REQUIRED] === 'true') cls.authRequired = true;
  if (ann[ANN_REQUIRES_CONFIRM] === 'true') cls.requiresConfirmation = true;
  const perms = splitCSV(ann[ANN_PERMISSIONS]);
  if (perms) cls.permissions = perms;
  const codes = splitCSV(ann[ANN_EXIT_CODES]);
  if (codes) cls.exitCodes = codes;
  return cls;
}

/** Parses a comma-separated annotation value, dropping empty entries. */
function splitCSV(s: string | undefined): string[] | undefined {
  if (!s) return undefined;
  const out = s
    .split(',')
    .map((p) => p.trim())
    .filter((p) => p !== '');
  return out.length > 0 ? out : undefined;
}

/**
 * Walks the tree depth-first, invoking `visit` for each leaf (a
 * command with no subcommands). The root's own name is not part of
 * any path.
 */
function walkLeaves(
  cmd: Command,
  path: string[],
  visit: (cmd: Command, path: string[]) => void,
): void {
  const subs = cmd.commands as Command[];
  if (subs.length === 0) {
    if (path.length > 0) visit(cmd, path);
    return;
  }
  for (const sub of subs) {
    walkLeaves(sub, [...path, sub.name()], visit);
  }
}

/** Resolves a leaf path back to its commander Command. */
function findCommand(root: Command, path: string[]): Command | undefined {
  let cur: Command = root;
  for (const seg of path) {
    const next = (cur.commands as Command[]).find((c) => c.name() === seg);
    if (next === undefined) return undefined;
    cur = next;
  }
  return cur;
}

/**
 * Maps a command's options to JSON Schema properties plus the
 * required-name list, filtering out hidden options. Long-flag names
 * are used verbatim (`--dry-run` → `dry-run`), matching the Go
 * surface's use of the pflag name rather than a camelCased accessor.
 */
export function collectFlags(cmd: Command): {
  properties: Record<string, FlagSchema>;
  required?: string[];
} {
  const properties: Record<string, FlagSchema> = {};
  const required: string[] = [];
  const seen = new Set<string>();

  for (const opt of cmd.options as Option[]) {
    if (opt.hidden) continue;
    const name = optionName(opt);
    if (name === '' || seen.has(name)) continue;
    seen.add(name);
    properties[name] = flagProperty(opt);
    if (opt.mandatory) required.push(name);
  }

  return required.length > 0 ? { properties, required } : { properties };
}

/** The schema property name for an option: its long flag, undashed. */
function optionName(opt: Option): string {
  const long = opt.long;
  if (long) return long.replace(/^--/, '');
  return (opt.short ?? '').replace(/^-/, '');
}

/** Maps one option to a JSON Schema property object. */
function flagProperty(opt: Option): FlagSchema {
  const type = optionJSONType(opt);
  const prop: FlagSchema = {
    type,
    description: opt.description ?? '',
  };
  if (type === 'array') prop.items = { type: 'string' };
  return prop;
}

/**
 * Maps a commander option to a JSON Schema primitive. Variadic
 * options are arrays; boolean flags are booleans; everything else is
 * a string unless the adopter declared a numeric parser.
 */
function optionJSONType(opt: Option): string {
  if (opt.variadic) return 'array';
  if (typeof opt.isBoolean === 'function' && opt.isBoolean()) return 'boolean';
  return 'string';
}
