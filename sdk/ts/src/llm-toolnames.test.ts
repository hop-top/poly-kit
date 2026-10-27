/**
 * Unit tests for tool-name rules, the sanitizer and the per-request
 * table. The cross-language vectors run in
 * llm-toolnames.conformance.test.ts; these cover what JSON vectors
 * cannot carry (lone surrogates), the error type and the mode param.
 */

import { describe, expect, it } from "vitest";
import {
  ANTHROPIC_TOOL_NAMES,
  GEMINI_TOOL_NAMES,
  LLMError,
  OPENAI_TOOL_NAMES,
  ToolNameError,
  ToolNames,
  isFallbackable,
  isValidToolName,
  sanitizeToolName,
  toolNameModeOf,
} from "./llm";

function thrown(fn: () => unknown): ToolNameError {
  try {
    fn();
  } catch (e) {
    expect(e).toBeInstanceOf(ToolNameError);
    return e as ToolNameError;
  }
  throw new Error("expected a ToolNameError");
}

describe("sanitizeToolName", () => {
  it("matches the spec examples", () => {
    expect(sanitizeToolName(OPENAI_TOOL_NAMES, "pane.open")).toBe("pane_open");
    expect(sanitizeToolName(OPENAI_TOOL_NAMES, "mcp://fs/read_file")).toBe(
      "mcp_fs_read_file"
    );
    expect(sanitizeToolName(OPENAI_TOOL_NAMES, "café.menu")).toBe("caf_menu");
    expect(sanitizeToolName(OPENAI_TOOL_NAMES, "検索")).toBe("__1b6b1a6f");
    expect(sanitizeToolName(OPENAI_TOOL_NAMES, "a".repeat(65))).toBe(
      "a".repeat(55) + "_635361c4"
    );
    expect(sanitizeToolName(GEMINI_TOOL_NAMES, "pane.open")).toBe("pane.open");
    expect(sanitizeToolName(GEMINI_TOOL_NAMES, "1tool")).toBe("_1tool");
  });

  it("collapses a run of disallowed bytes to one underscore", () => {
    expect(sanitizeToolName(ANTHROPIC_TOOL_NAMES, "a..//::b")).toBe("a_b");
    // A 4-byte emoji is one run, not four separators.
    expect(sanitizeToolName(ANTHROPIC_TOOL_NAMES, "x🔧y")).toBe("x_y");
  });

  it("hashes the original UTF-8 bytes, 8 hex digits", () => {
    const out = sanitizeToolName(ANTHROPIC_TOOL_NAMES, "...");
    expect(out).toMatch(/^__[0-9a-f]{8}$/);
    expect(out).toBe("__ab5df625");
  });

  it("refuses empty and lone-surrogate names", () => {
    const empty = thrown(() => sanitizeToolName(ANTHROPIC_TOOL_NAMES, ""));
    expect(empty.reason).toBe("empty");
    for (const bad of ["a\uD800", "\uDC00b", "x\uDBFFy"]) {
      const err = thrown(() => sanitizeToolName(ANTHROPIC_TOOL_NAMES, bad));
      expect(err.reason).toBe("invalid_utf8");
      expect(err.toolName).toBe(bad);
      expect(err.provider).toBe("anthropic");
    }
    // A well-formed surrogate pair is fine.
    expect(sanitizeToolName(ANTHROPIC_TOOL_NAMES, "a🔧")).toBe("a_");
  });

  it("measures length in bytes, not UTF-16 units", () => {
    // 63 ASCII bytes + 2-byte é: 65 bytes, sanitized to 64 then valid.
    expect(sanitizeToolName(OPENAI_TOOL_NAMES, "a".repeat(63) + "é")).toBe(
      "a".repeat(63) + "_"
    );
    expect(isValidToolName(OPENAI_TOOL_NAMES, "a".repeat(64))).toBe(true);
    expect(isValidToolName(OPENAI_TOOL_NAMES, "a".repeat(65))).toBe(false);
  });
});

describe("ToolNames", () => {
  it("fails with a typed, non-fallbackable error", () => {
    const err = thrown(() =>
      ToolNames.build(ANTHROPIC_TOOL_NAMES, "sanitize", [
        { name: "pane.open" },
        { name: "pane:open" },
      ])
    );
    expect(err).toBeInstanceOf(LLMError);
    expect(err.reason).toBe("collision");
    expect(err.toolName).toBe("pane:open");
    expect(err.other).toBe("pane.open");
    expect(err.name).toBe("ToolNameError");
    expect(err.message).toContain("pane.open");
    expect(isFallbackable(err)).toBe(false);
  });

  it("maps history names forward per mode", () => {
    const san = ToolNames.build(OPENAI_TOOL_NAMES, "sanitize", []);
    expect(san.wire("pane.open")).toBe("pane_open");
    expect(san.wire("")).toBe("");
    expect(san.wire("a\uD800")).toBe("a\uD800");
    const strict = ToolNames.build(OPENAI_TOOL_NAMES, "strict", []);
    expect(strict.wire("pane.open")).toBe("pane.open");
  });

  it("maps calls back without touching the input", () => {
    const t = ToolNames.build(ANTHROPIC_TOOL_NAMES, "sanitize", [
      { name: "pane.open" },
    ]);
    const calls = [{ id: "1", name: "pane_open", arguments: {} }];
    expect(t.calls(calls)[0]?.name).toBe("pane.open");
    expect(calls[0]?.name).toBe("pane_open");
    expect(t.defs([{ name: "pane.open", description: "", parameters: {} }]))
      .toEqual([{ name: "pane_open", description: "", parameters: {} }]);
  });
});

describe("toolNameModeOf", () => {
  it("defaults to sanitize and accepts both modes", () => {
    expect(toolNameModeOf(undefined)).toBe("sanitize");
    expect(toolNameModeOf({})).toBe("sanitize");
    expect(toolNameModeOf({ tool_names: "sanitize" })).toBe("sanitize");
    expect(toolNameModeOf({ tool_names: " strict " })).toBe("strict");
  });

  it("refuses any other value", () => {
    expect(() => toolNameModeOf({ tool_names: "loose" })).toThrow(LLMError);
    expect(() => toolNameModeOf({ tool_names: "" })).toThrow(/tool_names/);
  });
});
