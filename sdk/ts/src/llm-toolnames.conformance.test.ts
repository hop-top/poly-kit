// Runs the provider-neutral `tool_names` section of
// contracts/llm-v1/vectors.json. Go is canonical; a failure here means
// this SDK drifted, not that a vector should change.
//
// The anthropic sections, `anthropic_tool_names` among them, run in
// llm-anthropic.conformance.test.ts; the openai and routellm adapters'
// share of the spec is covered in llm-openai.test.ts and
// routellm.toolcall.test.ts.

import { existsSync, readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

import {
  ANTHROPIC_TOOL_NAMES,
  GEMINI_TOOL_NAMES,
  OPENAI_TOOL_NAMES,
  ToolNameError,
  ToolNames,
  isValidToolName,
  sanitizeToolName,
  type ToolNameMode,
  type ToolNameRule,
} from "./llm";

interface ToolNamesVector {
  name: string;
  rule: string;
  mode?: string;
  tools: string[];
  want?: string[];
  calls?: string[];
  want_calls?: string[];
  error?: { reason: string; name: string; other: string };
}

const VECTOR_KEYS = new Set([
  "name", "rule", "mode", "tools", "want", "calls", "want_calls", "error",
]);
const ERROR_KEYS = new Set(["reason", "name", "other"]);

const RULES: Record<string, ToolNameRule> = {
  anthropic: ANTHROPIC_TOOL_NAMES,
  openai: OPENAI_TOOL_NAMES,
  gemini: GEMINI_TOOL_NAMES,
};

function locate(): string {
  let dir = dirname(fileURLToPath(import.meta.url));
  for (let i = 0; i < 10; i++) {
    const p = resolve(dir, "contracts/llm-v1/vectors.json");
    if (existsSync(p)) return p;
    dir = dirname(dir);
  }
  throw new Error("contracts/llm-v1/vectors.json not found");
}

const file = JSON.parse(readFileSync(locate(), "utf8")) as {
  version: string;
  tool_names: ToolNamesVector[];
};

describe("llm-v1 conformance: tool_names", () => {
  it("is v1 and not empty", () => {
    expect(file.version).toBe("v1");
    expect(file.tool_names.length).toBeGreaterThan(0);
  });

  for (const v of file.tool_names) {
    it(v.name, () => {
      for (const k of Object.keys(v)) expect(VECTOR_KEYS).toContain(k);
      for (const k of Object.keys(v.error ?? {})) expect(ERROR_KEYS).toContain(k);
      const rule = RULES[v.rule];
      expect(rule, `unknown rule ${v.rule}`).toBeDefined();
      if (!rule) return;
      const mode = (v.mode || "sanitize") as ToolNameMode;
      expect(["sanitize", "strict"]).toContain(mode);
      const defs = v.tools.map((name) => ({ name }));

      if (v.error) {
        let err: unknown;
        try {
          ToolNames.build(rule, mode, defs);
        } catch (e) {
          err = e;
        }
        expect(err).toBeInstanceOf(ToolNameError);
        const te = err as ToolNameError;
        expect(te.reason).toBe(v.error.reason);
        expect(te.toolName).toBe(v.error.name);
        expect(te.other).toBe(v.error.other);
        expect(te.provider).toBe(rule.provider);
        return;
      }

      const names = ToolNames.build(rule, mode, defs);
      const got = v.tools.map((n) => names.wire(n));
      expect(got).toEqual(v.want);
      for (const w of got) expect(isValidToolName(rule, w)).toBe(true);
      if (mode === "sanitize") {
        v.tools.forEach((n, i) =>
          expect(sanitizeToolName(rule, n)).toBe(v.want?.[i])
        );
      }
      expect((v.calls ?? []).map((c) => names.original(c))).toEqual(
        v.want_calls ?? []
      );
    });
  }
});
