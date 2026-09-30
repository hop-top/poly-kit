/**
 * Tests for tool-call linkage and content parts on llm.Message.
 * Covers: checkToolLinkage role rules, Client pass-through, media sources.
 */

import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  LLMError,
  checkToolLinkage,
  clearRegistry,
  createLLM,
  fileSource,
  inlineSource,
  register,
  urlSource,
  type ContentPart,
  type Message,
  type Request,
  type ToolCall,
} from "./llm";

const call: ToolCall = {
  id: "call_1",
  name: "get_weather",
  arguments: { city: "NYC" },
};

// ─── checkToolLinkage ───────────────────────────────────────────────────────

describe("checkToolLinkage", () => {
  it("accepts messages that set neither field", () => {
    for (const role of ["system", "user", "assistant", "tool"]) {
      expect(() => checkToolLinkage({ role, content: "x" })).not.toThrow();
    }
  });

  it("accepts toolCalls on assistant with empty content", () => {
    expect(() =>
      checkToolLinkage({ role: "assistant", content: "", toolCalls: [call] })
    ).not.toThrow();
  });

  it("accepts toolCallId on tool", () => {
    expect(() =>
      checkToolLinkage({ role: "tool", content: "sunny", toolCallId: "call_1" })
    ).not.toThrow();
  });

  it("treats an empty toolCalls array as unset", () => {
    expect(() =>
      checkToolLinkage({ role: "user", content: "x", toolCalls: [] })
    ).not.toThrow();
  });

  it("rejects toolCalls on a tool result", () => {
    expect(() =>
      checkToolLinkage({
        role: "tool",
        content: "",
        toolCallId: "call_1",
        toolCalls: [call],
      })
    ).toThrow(/tool result.*only content/);
  });

  it.each(["user", "system"])(
    "rejects toolCalls on role %s",
    (role) => {
      expect(() =>
        checkToolLinkage({ role, content: "", toolCalls: [call] })
      ).toThrow(LLMError);
      expect(() =>
        checkToolLinkage({ role, content: "", toolCalls: [call] })
      ).toThrow(/toolCalls.*only on role assistant/);
    }
  );

  it.each(["user", "system", "assistant"])(
    "rejects toolCallId on role %s",
    (role) => {
      expect(() =>
        checkToolLinkage({ role, content: "x", toolCallId: "call_1" })
      ).toThrow(/toolCallId.*only on role tool/);
    }
  );

  it("rejects a tool result carrying parts", () => {
    const m: Message = {
      role: "tool",
      content: "",
      toolCallId: "call_1",
      parts: [{ type: "text", text: "sunny" }],
    };
    expect(() => checkToolLinkage(m)).toThrow(/tool result.*only content/);
  });
});

// ─── Client pass-through ────────────────────────────────────────────────────

describe("Client carries linkage to the provider", () => {
  afterEach(() => {
    clearRegistry();
  });

  it("hands toolCalls and toolCallId to callWithTools unchanged", async () => {
    let got: Request | undefined;
    register("linkmock", () => ({
      callWithTools: async (req) => {
        got = req;
        return { content: "ok", toolCalls: [] };
      },
      close: vi.fn(),
    }));

    const messages: Message[] = [
      { role: "user", content: "Weather?" },
      { role: "assistant", content: "", toolCalls: [call] },
      { role: "tool", content: "sunny", toolCallId: "call_1" },
    ];
    const client = createLLM("linkmock://m");
    await client.callWithTools({ messages }, [
      { name: "get_weather", description: "", parameters: {} },
    ]);

    expect(got?.messages[1].toolCalls).toEqual([call]);
    expect(got?.messages[2].toolCallId).toBe("call_1");
  });
});

// ─── Media sources ──────────────────────────────────────────────────────────

describe("media sources", () => {
  it("inlineSource returns its bytes and MIME type, no URL", async () => {
    const src = inlineSource(new Uint8Array([1, 2, 3]), "image/png");
    expect(src.url()).toBe("");
    expect(src.mimeType()).toBe("image/png");
    expect(Array.from(await src.read())).toEqual([1, 2, 3]);
  });

  it("urlSource exposes its URL and no MIME type", () => {
    const src = urlSource("https://example.test/cat.png");
    expect(src.url()).toBe("https://example.test/cat.png");
    expect(src.mimeType()).toBe("");
  });

  it("fileSource infers MIME from extension and reads the file", async () => {
    const dir = mkdtempSync(join(tmpdir(), "kit-llm-"));
    try {
      const p = join(dir, "cat.PNG");
      writeFileSync(p, Buffer.from([9, 8]));
      const src = fileSource(p);
      expect(src.url()).toBe("");
      expect(src.mimeType()).toBe("image/png");
      expect(Array.from(await src.read())).toEqual([9, 8]);
      expect(fileSource(join(dir, "notes")).mimeType()).toBe("");
      expect(fileSource(join(dir, "x.unknownext")).mimeType()).toBe("");
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  });

  it("a ContentPart carries type, text, source, mimeType, metadata", () => {
    const part: ContentPart = {
      type: "image",
      source: urlSource("https://example.test/cat.png"),
      mimeType: "image/png",
      metadata: { alt: "cat" },
    };
    const m: Message = { role: "user", content: "", parts: [part] };
    expect(m.parts?.[0].type).toBe("image");
  });
});
