/**
 * Tool-name rules, the reversible sanitizer and the per-request name
 * table, re-exported from `@hop-top/kit/llm`.
 *
 * Providers restrict tool names; callers use ids such as `pane.open`.
 * A tool-calling adapter builds a {@link ToolNames} per request, sends
 * wire names and returns the caller's names. Normative spec, shared by
 * every kit SDK: `contracts/llm-v1/tool-names.md`.
 */

import { createHash } from "node:crypto";

import { LLMError } from "./llm-error";

/** Provider param selecting the {@link ToolNameMode}: `?tool_names=strict`. */
export const TOOL_NAMES_PARAM = "tool_names";

/**
 * How an adapter treats a name its provider does not accept.
 * `sanitize` (default) rewrites it and maps it back; `strict` sends
 * names as given and fails an invalid one before sending.
 */
export type ToolNameMode = "sanitize" | "strict";

/** Why a {@link ToolNameError} fails a request. */
export type ToolNameReason =
  | "empty"
  | "invalid_utf8"
  | "invalid"
  | "duplicate"
  | "collision";

/**
 * A provider's tool-name rule: 1 to `maxLen` bytes of ASCII letters,
 * digits, `_`, `-` and the bytes of `extra`; with `leadLetter`, the
 * first byte is a letter or `_`.
 */
export interface ToolNameRule {
  /** Names the rule in errors. */
  readonly provider: string;
  readonly maxLen: number;
  readonly extra: string;
  readonly leadLetter: boolean;
}

/** Anthropic Messages API: `^[a-zA-Z0-9_-]{1,128}$`. */
export const ANTHROPIC_TOOL_NAMES: ToolNameRule = Object.freeze({
  provider: "anthropic",
  maxLen: 128,
  extra: "",
  leadLetter: false,
});

/** OpenAI Chat Completions and compatible servers: `^[a-zA-Z0-9_-]{1,64}$`. */
export const OPENAI_TOOL_NAMES: ToolNameRule = Object.freeze({
  provider: "openai",
  maxLen: 64,
  extra: "",
  leadLetter: false,
});

/** Gemini `FunctionDeclaration.name`: `^[a-zA-Z_][a-zA-Z0-9_.:-]{0,127}$`. */
export const GEMINI_TOOL_NAMES: ToolNameRule = Object.freeze({
  provider: "gemini",
  maxLen: 128,
  extra: ".:",
  leadLetter: true,
});

/**
 * A tool name fails the request before anything is sent. Not
 * fallbackable: the caller's tool set needs fixing. `toolName` is the
 * offending name (`name` is the error class name, as on every Error);
 * `other` is the earlier tool a `collision` maps to the same wire name.
 */
export class ToolNameError extends LLMError {
  readonly provider: string;
  readonly reason: ToolNameReason;
  readonly toolName: string;
  readonly other: string;

  constructor(
    provider: string,
    reason: ToolNameReason,
    toolName: string,
    other = ""
  ) {
    super(toolNameMessage(provider, reason, toolName, other));
    this.name = "ToolNameError";
    this.provider = provider;
    this.reason = reason;
    this.toolName = toolName;
    this.other = other;
  }
}

function toolNameMessage(
  provider: string,
  reason: ToolNameReason,
  name: string,
  other: string
): string {
  const q = JSON.stringify;
  switch (reason) {
    case "empty":
      return `tool name is empty (provider ${q(provider)})`;
    case "invalid_utf8":
      return `tool name ${q(name)} is not valid UTF-8 (provider ${q(provider)})`;
    case "invalid":
      return `tool name ${q(name)} is not valid for provider ${q(provider)}`;
    case "duplicate":
      return `tool name ${q(name)} is defined twice (provider ${q(provider)})`;
    case "collision":
      return `tool names ${q(other)} and ${q(name)} map to the same provider name (provider ${q(provider)})`;
  }
}

/**
 * Reads {@link TOOL_NAMES_PARAM} from provider params; absent means
 * `sanitize`. Any other value throws, failing adapter construction.
 */
export function toolNameModeOf(
  params: Record<string, string> | undefined
): ToolNameMode {
  const v = params?.[TOOL_NAMES_PARAM];
  if (v === undefined) return "sanitize";
  const m = v.trim();
  if (m === "sanitize" || m === "strict") return m;
  throw new LLMError(
    `${TOOL_NAMES_PARAM} must be "sanitize" or "strict", got ${JSON.stringify(v)}`
  );
}

/** Hex digits of SHA-256 a shortened name ends with, after a `_`. */
const HASH_LEN = 8;

const UNDERSCORE = 0x5f;
const encoder = new TextEncoder();
const decoder = new TextDecoder();
const LONE_SURROGATE =
  /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/;

function isAlnum(c: number): boolean {
  return (
    (c >= 0x61 && c <= 0x7a) || (c >= 0x41 && c <= 0x5a) || (c >= 0x30 && c <= 0x39)
  );
}

function allowed(rule: ToolNameRule, c: number): boolean {
  if (isAlnum(c) || c === UNDERSCORE || c === 0x2d) return true;
  return c < 0x80 && rule.extra.includes(String.fromCharCode(c));
}

function leadOK(rule: ToolNameRule, c: number): boolean {
  if (!rule.leadLetter) return true;
  return c === UNDERSCORE || (c >= 0x61 && c <= 0x7a) || (c >= 0x41 && c <= 0x5a);
}

function validBytes(rule: ToolNameRule, b: Uint8Array): boolean {
  if (b.length === 0 || b.length > rule.maxLen || !leadOK(rule, b[0]!)) {
    return false;
  }
  for (const c of b) if (!allowed(rule, c)) return false;
  return true;
}

/** Sanitizer output is ASCII bytes; no spread, so long names cannot overflow. */
function ascii(b: ArrayLike<number>): string {
  return decoder.decode(Uint8Array.from(b));
}

/** Reports whether the provider accepts `name` as is. */
export function isValidToolName(rule: ToolNameRule, name: string): boolean {
  if (LONE_SURROGATE.test(name)) return false;
  return validBytes(rule, encoder.encode(name));
}

/**
 * Returns `name` as the provider accepts it; a valid name comes back
 * unchanged. Otherwise, over the UTF-8 bytes of `name`: each maximal
 * run of disallowed bytes becomes one `_`; with `leadLetter`, a bad
 * first byte gains a leading `_`; a result over `maxLen`, or with no
 * ASCII letter or digit, keeps its first `maxLen - 9` bytes and gains
 * `_` + 8 hex of SHA-256(name). Throws {@link ToolNameError} for an
 * empty name (`empty`) or a lone surrogate (`invalid_utf8`).
 */
export function sanitizeToolName(rule: ToolNameRule, name: string): string {
  if (name === "") throw new ToolNameError(rule.provider, "empty", name);
  if (LONE_SURROGATE.test(name)) {
    throw new ToolNameError(rule.provider, "invalid_utf8", name);
  }
  const src = encoder.encode(name);
  if (validBytes(rule, src)) return name;

  const out: number[] = [];
  let run = false;
  for (const c of src) {
    if (allowed(rule, c)) {
      out.push(c);
      run = false;
      continue;
    }
    if (!run) out.push(UNDERSCORE);
    run = true;
  }
  if (!leadOK(rule, out[0]!)) out.unshift(UNDERSCORE);
  if (out.length <= rule.maxLen && out.some(isAlnum)) return ascii(out);

  const hash = createHash("sha256").update(src).digest("hex").slice(0, HASH_LEN);
  const keep = Math.min(out.length, rule.maxLen - HASH_LEN - 1);
  return `${ascii(out.slice(0, keep))}_${hash}`;
}

/**
 * One request's tool names: original -> wire and back. Adapters build
 * one per request with {@link ToolNames.build}, send
 * {@link ToolNames.wire} names (tools and replayed history) and return
 * calls through {@link ToolNames.calls}.
 */
export class ToolNames {
  private readonly toWire = new Map<string, string>();
  private readonly toOrig = new Map<string, string>();

  private constructor(
    readonly rule: ToolNameRule,
    readonly mode: ToolNameMode
  ) {}

  /**
   * Checks `defs` against `rule` and builds their mapping. The first
   * failing def, in order, throws a {@link ToolNameError}: `empty`,
   * `invalid_utf8`, `invalid` (strict only), `duplicate`, `collision`
   * (`other` names the earlier def with the same wire name).
   */
  static build(
    rule: ToolNameRule,
    mode: ToolNameMode,
    defs: ReadonlyArray<{ readonly name: string }>
  ): ToolNames {
    const t = new ToolNames(rule, mode);
    for (const { name } of defs) {
      let wire: string;
      if (name === "") {
        throw new ToolNameError(rule.provider, "empty", name);
      } else if (LONE_SURROGATE.test(name)) {
        throw new ToolNameError(rule.provider, "invalid_utf8", name);
      } else if (mode === "strict") {
        if (!isValidToolName(rule, name)) {
          throw new ToolNameError(rule.provider, "invalid", name);
        }
        wire = name;
      } else {
        wire = sanitizeToolName(rule, name);
      }
      if (t.toWire.has(name)) {
        throw new ToolNameError(rule.provider, "duplicate", name);
      }
      const other = t.toOrig.get(wire);
      if (other !== undefined) {
        throw new ToolNameError(rule.provider, "collision", name, other);
      }
      t.toWire.set(name, wire);
      t.toOrig.set(wire, name);
    }
    return t;
  }

  /**
   * The name to send: a def's wire name; otherwise, as for a call
   * replayed from history, `name` sanitized (`sanitize`) or unchanged
   * (`strict`, or a name the sanitizer refuses).
   */
  wire(name: string): string {
    const w = this.toWire.get(name);
    if (w !== undefined) return w;
    if (this.mode === "strict") return name;
    try {
      return sanitizeToolName(this.rule, name);
    } catch {
      return name;
    }
  }

  /** The def name a returned wire name stands for; unknown names pass through. */
  original(wire: string): string {
    return this.toOrig.get(wire) ?? wire;
  }

  /** `defs` with each name replaced by its wire name; input untouched. */
  defs<T extends { name: string }>(defs: ReadonlyArray<T>): T[] {
    return defs.map((d) => ({ ...d, name: this.wire(d.name) }));
  }

  /** `calls` with each name replaced by its original; input untouched. */
  calls<T extends { name: string }>(calls: ReadonlyArray<T>): T[] {
    return calls.map((c) => ({ ...c, name: this.original(c.name) }));
  }
}
