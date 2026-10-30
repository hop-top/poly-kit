/**
 * Internal helpers shared by the toolspec modules. Not re-exported
 * from the package's `toolspec` entry.
 */

import type {
  Command as CommanderCommand,
  Option as CommanderOption,
} from "commander";

export type Annotations = Record<string, string>;

export function annotationsOf(cmd: CommanderCommand): Annotations {
  return (cmd as { annotations?: Annotations }).annotations ?? {};
}

export function splitCSV(s: string | undefined): string[] | undefined {
  if (!s) return undefined;
  const out = s.split(",").map((p) => p.trim()).filter((p) => p !== "");
  return out.length > 0 ? out : undefined;
}

// --- commander facts ---

/**
 * cmd's declared positional arguments (commander registeredArguments),
 * in order, `required` set only on required ones. Shared by the
 * toolspec projection and the MCP bridge so both read positionals
 * the same way.
 */
export function declaredArgs(
  cmd: CommanderCommand,
): Array<{ name: string; required?: boolean }> {
  return cmd.registeredArguments.map((a) =>
    a.required ? { name: a.name(), required: true } : { name: a.name() },
  );
}

/**
 * Whether opt takes no value: a plain boolean flag or a negated one
 * (`--no-x`). commander's isBoolean() is false for negated options,
 * yet they are value-less all the same, so pflag-shaped projections
 * (a `no-x` bool) must not rely on isBoolean() alone.
 */
export function takesNoValue(opt: CommanderOption): boolean {
  return !opt.required && !opt.optional;
}

/**
 * Whether cmd has an action handler (cobra Runnable). commander 15
 * exposes no public accessor, so this reads the private field.
 */
export function isRunnable(cmd: CommanderCommand): boolean {
  return (cmd as unknown as { _actionHandler?: unknown })._actionHandler != null;
}

/** Hidden per the parent's help (public API; no private _hidden read). */
export function isHidden(cmd: CommanderCommand, parent: CommanderCommand): boolean {
  return !parent.createHelp().visibleCommands(parent).includes(cmd);
}

export const BUILTIN_NAMES = new Set([
  "help",
  "completion",
  "man",
  "__complete",
  "__completeNoDesc",
]);

export function isBuiltin(cmd: CommanderCommand, parent: CommanderCommand): boolean {
  return BUILTIN_NAMES.has(cmd.name()) || parent.name() === "completion";
}

export function byName(a: { name(): string }, b: { name(): string }): number {
  const x = a.name();
  const y = b.name();
  return x < y ? -1 : x > y ? 1 : 0;
}

// --- Go encoding/json byte parity ---

/** Go's json string escaping (HTML-safe, U+2028/2029 escaped). */
export function goQuote(s: string): string {
  return JSON.stringify(s)
    .replace(/</g, "\\u003c")
    .replace(/>/g, "\\u003e")
    .replace(/&/g, "\\u0026")
    .replace(/\u2028/g, "\\u2028")
    .replace(/\u2029/g, "\\u2029");
}

/**
 * Re-emit raw JSON text the way Go's Encoder.SetIndent("", "  ")
 * does for an embedded RawJSON: tokens verbatim (numbers keep their
 * spelling), insignificant whitespace dropped, strings HTML-escaped,
 * structure re-indented at the given depth. Throws on invalid JSON,
 * as Go's encoder errors.
 */
export function reindentRaw(raw: string, depth: number): string {
  JSON.parse(raw); // validity gate
  let out = "";
  let level = depth;
  let needIndent = false;
  const nl = (lvl: number) => "\n" + "  ".repeat(lvl);
  for (let i = 0; i < raw.length; i++) {
    const c = raw[i];
    if (c === " " || c === "\t" || c === "\n" || c === "\r") continue;
    if (needIndent && c !== "]" && c !== "}") {
      out += nl(level);
      needIndent = false;
    }
    if (c === '"') {
      let j = i + 1;
      while (raw[j] !== '"') j += raw[j] === "\\" ? 2 : 1;
      out += goQuote(JSON.parse(raw.slice(i, j + 1)) as string);
      i = j;
      continue;
    }
    switch (c) {
      case "{":
      case "[":
        out += c;
        level++;
        needIndent = true;
        break;
      case "}":
      case "]":
        level--;
        if (needIndent) needIndent = false;
        else out += nl(level);
        out += c;
        break;
      case ",":
        out += c + nl(level);
        break;
      case ":":
        out += ": ";
        break;
      default:
        out += c;
    }
  }
  return out;
}

/** Go encoding/json (Encoder.SetIndent("", "  ")) bytes for v; keys in rawKeys hold raw JSON text. */
export function encodeGoJSON(v: unknown, depth: number, rawKeys: Set<string>, key?: string): string {
  if (key !== undefined && rawKeys.has(key) && typeof v === "string") {
    return reindentRaw(v, depth);
  }
  if (typeof v === "string") return goQuote(v);
  if (typeof v === "boolean" || typeof v === "number") return JSON.stringify(v);
  if (Array.isArray(v)) {
    if (v.length === 0) return "[]";
    const pad = "  ".repeat(depth + 1);
    const items = v.map((x) => pad + encodeGoJSON(x, depth + 1, rawKeys));
    return "[\n" + items.join(",\n") + "\n" + "  ".repeat(depth) + "]";
  }
  if (v && typeof v === "object") {
    const entries = Object.entries(v).filter(([, x]) => x !== undefined);
    if (entries.length === 0) return "{}";
    const pad = "  ".repeat(depth + 1);
    const items = entries.map(
      ([k, x]) => pad + goQuote(k) + ": " + encodeGoJSON(x, depth + 1, rawKeys, k),
    );
    return "{\n" + items.join(",\n") + "\n" + "  ".repeat(depth) + "}";
  }
  return "null";
}

/**
 * Visit every command below program depth-first, children in name
 * order (cobra's default sorting), with its Go-style path (root name
 * first) and whether it is reserved: its depth-1 verb is in
 * `reserved`, or it or an ancestor carries kit/reserved=true.
 */
export function walkCommands(
  program: CommanderCommand,
  reserved: Iterable<string> | undefined,
  visit: (
    cmd: CommanderCommand,
    parent: CommanderCommand,
    path: string[],
    reserved: boolean,
  ) => void,
): void {
  const reservedSet = new Set(reserved ?? []);
  const walk = (
    cmd: CommanderCommand,
    parent: CommanderCommand,
    path: string[],
    depthOne: string,
    reservedTree: boolean,
  ): void => {
    const inReserved = reservedTree || annotationsOf(cmd)["kit/reserved"] === "true";
    visit(cmd, parent, path, inReserved || reservedSet.has(depthOne));
    for (const child of [...cmd.commands].sort(byName)) {
      if (child.name() === "") continue;
      walk(child, cmd, [...path, child.name()], depthOne, inReserved);
    }
  };
  for (const child of [...program.commands].sort(byName)) {
    if (child.name() === "") continue;
    walk(child, program, [program.name(), child.name()], child.name(), false);
  }
}

/**
 * pflag type names for options whose commander shape cannot say
 * (commander has no typed options), e.g. a float --min.
 */
export const pflagTypes = new WeakMap<CommanderOption, string>();

/** Go fmt "%.0f": round half to even. */
export function goFormat0(x: number): string {
  const floor = Math.floor(x);
  const diff = x - floor;
  let r = diff > 0.5 ? floor + 1 : diff < 0.5 ? floor : floor % 2 === 0 ? floor : floor + 1;
  if (Object.is(r, -0)) r = 0;
  return String(r);
}
