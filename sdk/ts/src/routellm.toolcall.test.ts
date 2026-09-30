/**
 * Tests for how the routellm adapter serializes messages onto the
 * OpenAI-compatible wire: tool-call linkage, content parts, and the
 * errors it returns instead of degrading either to plain text.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  LLMError,
  inlineSource,
  urlSource,
  type Message,
  type ResolvedConfig,
  type ToolCall,
} from "./llm";
import { RouteLLMAdapter } from "./routellm";

type WireMessage = Record<string, unknown>;

const cfg: ResolvedConfig = {
  uri: { scheme: "routellm", model: "mf:0.7" },
  provider: { model: "mf:0.7", baseURL: "http://routellm.test" },
  fallbacks: [],
};

const call: ToolCall = {
  id: "call_1",
  name: "get_weather",
  arguments: { city: "NYC" },
};

let bodies: { messages: WireMessage[] }[];
const origFetch = globalThis.fetch;

function jsonResponse(): globalThis.Response {
  return new globalThis.Response(
    JSON.stringify({
      choices: [
        { message: { role: "assistant", content: "ok" }, finish_reason: "stop" },
      ],
    }),
    { status: 200, headers: { "Content-Type": "application/json" } }
  );
}

beforeEach(() => {
  delete process.env["ROUTELLM_BASE_URL"];
  bodies = [];
  globalThis.fetch = vi.fn(async (_url: unknown, init?: RequestInit) => {
    bodies.push(JSON.parse(String(init?.body)));
    return jsonResponse();
  }) as unknown as typeof globalThis.fetch;
});

afterEach(() => {
  globalThis.fetch = origFetch;
});

async function wire(messages: Message[]): Promise<WireMessage[]> {
  await new RouteLLMAdapter(cfg).complete({ messages });
  expect(bodies).toHaveLength(1);
  return bodies[0].messages;
}

async function rejects(messages: Message[], pattern: RegExp): Promise<void> {
  const p = new RouteLLMAdapter(cfg).complete({ messages });
  await expect(p).rejects.toThrow(LLMError);
  await expect(
    new RouteLLMAdapter(cfg).complete({ messages })
  ).rejects.toThrow(pattern);
  expect(bodies).toHaveLength(0);
}

// ─── Tool-call linkage ──────────────────────────────────────────────────────

describe("routellm tool-call linkage on the wire", () => {
  it("sends assistant tool_calls and a linked tool result", async () => {
    const msgs = await wire([
      { role: "user", content: "Weather?" },
      { role: "assistant", content: "", toolCalls: [call] },
      { role: "tool", content: "sunny", toolCallId: "call_1" },
    ]);

    expect(msgs).toHaveLength(3);
    expect(msgs[0]).toEqual({ role: "user", content: "Weather?" });
    expect(msgs[1]).toEqual({
      role: "assistant",
      tool_calls: [
        {
          id: "call_1",
          type: "function",
          function: { name: "get_weather", arguments: '{"city":"NYC"}' },
        },
      ],
    });
    expect(msgs[2]).toEqual({
      role: "tool",
      content: "sunny",
      tool_call_id: "call_1",
    });
  });

  it("keeps assistant text sent alongside tool calls", async () => {
    const msgs = await wire([
      { role: "assistant", content: "Checking.", toolCalls: [call] },
    ]);
    expect(msgs[0]["content"]).toBe("Checking.");
  });

  it("encodes arguments: JSON string as is, value stringified, none as {}", async () => {
    const msgs = await wire([
      {
        role: "assistant",
        content: "",
        toolCalls: [
          { id: "a", name: "f", arguments: '{"x":1}' },
          { id: "b", name: "f", arguments: { y: [2] } },
          { id: "c", name: "f", arguments: undefined },
        ],
      },
    ]);
    const calls = msgs[0]["tool_calls"] as {
      function: { arguments: string };
    }[];
    expect(calls.map((c) => c.function.arguments)).toEqual([
      '{"x":1}',
      '{"y":[2]}',
      "{}",
    ]);
  });

  it("maps messages without the new fields exactly as before", async () => {
    const msgs = await wire([
      { role: "system", content: "be brief" },
      { role: "user", content: "hi" },
      { role: "assistant", content: "hello" },
    ]);
    expect(msgs).toEqual([
      { role: "system", content: "be brief" },
      { role: "user", content: "hi" },
      { role: "assistant", content: "hello" },
    ]);
  });

  it("stream sends the same linkage", async () => {
    globalThis.fetch = vi.fn(async (_url: unknown, init?: RequestInit) => {
      bodies.push(JSON.parse(String(init?.body)));
      return new globalThis.Response("data: [DONE]\n\n", { status: 200 });
    }) as unknown as typeof globalThis.fetch;

    const adapter = new RouteLLMAdapter(cfg);
    for await (const tok of adapter.stream({
      messages: [
        { role: "assistant", content: "", toolCalls: [call] },
        { role: "tool", content: "sunny", toolCallId: "call_1" },
      ],
    })) {
      expect(tok.done).toBe(true);
    }
    expect(bodies[0].messages[1]).toEqual({
      role: "tool",
      content: "sunny",
      tool_call_id: "call_1",
    });
  });

  it("rejects a tool result without toolCallId", async () => {
    await rejects(
      [{ role: "tool", content: "sunny" }],
      /tool result needs toolCallId/
    );
  });

  it("checks a missing toolCallId before other tool-result faults", async () => {
    await rejects(
      [{ role: "tool", content: "", parts: [{ type: "text", text: "x" }] }],
      /tool result needs toolCallId/
    );
  });

  it("rejects a tool call without an id", async () => {
    await rejects(
      [{ role: "assistant", content: "", toolCalls: [{ ...call, id: "" }] }],
      /tool call "get_weather" needs an id/
    );
  });

  it("rejects arguments that are not valid JSON", async () => {
    await rejects(
      [
        {
          role: "assistant",
          content: "",
          toolCalls: [{ ...call, arguments: "{city:" }],
        },
      ],
      /arguments are not valid JSON/
    );
  });

  it("rejects toolCallId on a non-tool role", async () => {
    await rejects(
      [{ role: "user", content: "x", toolCallId: "call_1" }],
      /toolCallId.*only on role tool/
    );
  });

  it("rejects toolCalls on a non-assistant role", async () => {
    await rejects(
      [{ role: "user", content: "x", toolCalls: [call] }],
      /toolCalls.*only on role assistant/
    );
  });

  it("rejects a tool result carrying parts", async () => {
    await rejects(
      [
        {
          role: "tool",
          content: "",
          toolCallId: "call_1",
          parts: [{ type: "text", text: "sunny" }],
        },
      ],
      /tool result.*only content/
    );
  });

  it("rejects assistant tool calls with parts", async () => {
    await rejects(
      [
        {
          role: "assistant",
          content: "",
          toolCalls: [call],
          parts: [{ type: "text", text: "x" }],
        },
      ],
      /assistant tool calls do not support content parts/
    );
  });
});

// ─── Content parts ──────────────────────────────────────────────────────────

describe("routellm content parts on the wire", () => {
  it("sends text and URL image parts as a content array", async () => {
    const msgs = await wire([
      {
        role: "user",
        content: "ignored when parts are set",
        parts: [
          { type: "text", text: "What is this?" },
          { type: "image", source: urlSource("https://example.test/cat.png") },
        ],
      },
    ]);
    expect(msgs[0]).toEqual({
      role: "user",
      content: [
        { type: "text", text: "What is this?" },
        {
          type: "image_url",
          image_url: { url: "https://example.test/cat.png" },
        },
      ],
    });
  });

  it("inlines non-URL images as a base64 data URI", async () => {
    const msgs = await wire([
      {
        role: "user",
        content: "",
        parts: [
          {
            type: "image",
            source: inlineSource(new Uint8Array([1, 2, 3]), "image/png"),
          },
        ],
      },
    ]);
    const content = msgs[0]["content"] as WireMessage[];
    expect(content[0]).toEqual({
      type: "image_url",
      image_url: { url: "data:image/png;base64,AQID" },
    });
  });

  it("sends a PDF as a file part", async () => {
    const msgs = await wire([
      {
        role: "user",
        content: "",
        parts: [
          {
            type: "image",
            mimeType: "application/pdf",
            source: inlineSource(new Uint8Array([1, 2, 3]), ""),
          },
        ],
      },
    ]);
    const content = msgs[0]["content"] as WireMessage[];
    expect(content[0]).toEqual({ type: "file", file: { file_data: "AQID" } });
  });

  it("rejects an image with no URL and no MIME type", async () => {
    await rejects(
      [
        {
          role: "user",
          content: "",
          parts: [{ type: "image", source: inlineSource(new Uint8Array(), "") }],
        },
      ],
      /image part needs a MIME type/
    );
  });

  it("rejects modalities the wire cannot carry", async () => {
    await rejects(
      [
        {
          role: "user",
          content: "",
          parts: [
            { type: "audio", source: inlineSource(new Uint8Array(), "audio/mpeg") },
          ],
        },
      ],
      /unsupported modality "audio"/
    );
  });

  it("rejects parts on a non-user role", async () => {
    await rejects(
      [{ role: "system", content: "", parts: [{ type: "text", text: "x" }] }],
      /role "system" does not support content parts/
    );
  });
});
