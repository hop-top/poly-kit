/**
 * @module mcp/args
 * @package @hop-top/kit
 *
 * Positional arguments over MCP, ported from Go's
 * go/transport/cmdsurface/surface_mcp_args.go.
 *
 * An MCP tool takes one JSON object of named arguments; a leaf takes
 * flags by name and positional arguments by position. A leaf that
 * declares its positionals (`Leaf.args`) publishes them as ONE extra
 * property, "args": an array of strings in declared order — the key
 * and shape every kit surface uses for positionals. One property per
 * argument would share a namespace with the leaf's flags, so a flag
 * added later could silently take an argument's name; the array
 * reserves exactly one name, and a trailing variadic argument needs
 * no extra syntax.
 *
 * The property is required, with minItems set to the number of
 * required arguments, when any argument is; optional arguments only
 * lengthen what the array may hold. No maxItems is published, as in
 * Go: the command's own parser refuses a count it does not accept.
 *
 * A leaf whose "args" name is taken by a flag publishes no such
 * property and says so in its description: the flag was part of the
 * tool's schema first, and taking the name would change what an
 * existing caller's "args" means.
 */

import type { FlagSchema, Leaf, LeafArg } from './types.js';

/** The inputSchema property carrying a leaf's declared positionals. */
export const MCP_ARGS_PROPERTY = 'args';

/** Description note for a leaf whose positionals MCP cannot pass. */
const NOTE_ARGS_FLAG_TAKEN =
  'Takes positional arguments that cannot be passed over MCP: its --args flag holds the "args" property.';

/**
 * Reports whether leaf's inputSchema carries the "args" property: it
 * declares positional arguments and no flag already holds the name.
 */
export function mcpArgsPublished(leaf: Leaf): boolean {
  if (!leaf.args || leaf.args.length === 0) return false;
  return !Object.prototype.hasOwnProperty.call(
    leaf.properties ?? {},
    MCP_ARGS_PROPERTY,
  );
}

/**
 * The description leaf's MCP tool publishes: its own description,
 * followed by a note when it takes positionals the tool cannot pass.
 */
export function mcpToolDescription(leaf: Leaf): string {
  const desc = leaf.description;
  if (!leaf.args || leaf.args.length === 0 || mcpArgsPublished(leaf)) {
    return desc;
  }
  return desc === '' ? NOTE_ARGS_FLAG_TAKEN : `${desc} ${NOTE_ARGS_FLAG_TAKEN}`;
}

/**
 * leaf's MCP tool inputSchema: one property per flag, the leaf's
 * required flags listed as required, plus the "args" property when
 * {@link mcpArgsPublished}.
 */
export function mcpInputSchema(leaf: Leaf): Record<string, unknown> {
  const properties: Record<string, FlagSchema> = { ...(leaf.properties ?? {}) };
  const required = [...(leaf.required ?? [])];
  if (mcpArgsPublished(leaf)) {
    const declared = leaf.args ?? [];
    const prop: FlagSchema = {
      type: 'array',
      items: { type: 'string' },
      description: argsDescription(declared),
    };
    const n = requiredCount(declared);
    if (n > 0) {
      prop.minItems = n;
      required.push(MCP_ARGS_PROPERTY);
    }
    properties[MCP_ARGS_PROPERTY] = prop;
  }
  const schema: Record<string, unknown> = { type: 'object', properties };
  if (required.length > 0) schema.required = required;
  return schema;
}

/** The outcome of {@link mcpSplitArguments}. */
export type McpSplitResult =
  | { flags?: Record<string, unknown>; args?: string[]; error?: undefined }
  | { error: string };

/**
 * Splits a tools/call arguments object into an invocation's flags
 * and positional args. When leaf publishes "args" that key is taken
 * out of the flags: it must hold an array of strings, at least as
 * many as the leaf declares required, and becomes `args` in order.
 * Every other key is a flag, forwarded as-is. A leaf that publishes
 * no "args" property gets every key as a flag.
 *
 * `error` names the problem for the caller to correct; the handlers
 * return it as an isError tool result, before the confirmation gate.
 */
export function mcpSplitArguments(
  leaf: Leaf,
  args: Record<string, unknown> | undefined,
): McpSplitResult {
  if (!mcpArgsPublished(leaf)) return { flags: args };
  const declared = leaf.args ?? [];
  const src = args ?? {};
  if (!Object.prototype.hasOwnProperty.call(src, MCP_ARGS_PROPERTY)) {
    return requireArgs(declared, []) ?? { flags: args };
  }
  const flags: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(src)) {
    if (k !== MCP_ARGS_PROPERTY) flags[k] = v;
  }
  const raw = src[MCP_ARGS_PROPERTY];
  if (raw !== null && !Array.isArray(raw)) {
    return { error: '"args" must be an array of strings' };
  }
  const list = (raw ?? []) as unknown[];
  if (!list.every((v) => typeof v === 'string')) {
    return { error: '"args" must be an array of strings' };
  }
  const positional = list as string[];
  const missing = requireArgs(declared, positional);
  if (missing) return missing;
  return {
    flags: Object.keys(flags).length > 0 ? flags : undefined,
    args: positional,
  };
}

/**
 * Names the first declared-required argument not supplied, with the
 * wording every kit surface uses.
 */
function requireArgs(
  declared: LeafArg[],
  supplied: string[],
): { error: string } | undefined {
  if (supplied.length < requiredCount(declared)) {
    return {
      error: `missing required argument: ${declared[supplied.length]?.name ?? ''}`,
    };
  }
  return undefined;
}

function requiredCount(declared: LeafArg[]): number {
  return declared.filter((a) => a.required).length;
}

/** Declared names in order, optional ones marked with "?". */
function argsDescription(declared: LeafArg[]): string {
  const names = declared.map((a) => (a.required ? a.name : `${a.name}?`));
  return `Positional arguments in order: ${names.join(', ')}`;
}
