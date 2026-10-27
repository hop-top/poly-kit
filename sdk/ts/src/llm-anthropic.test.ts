/**
 * Anthropic adapter beyond the llm-v1 vectors: registry resolution and
 * key sources, the endpoint gate, and a tool round with thinking
 * replayed through the outcome.
 */

import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { KeyEndpointError, LLMError, createLLM, resolve, type Token } from "./llm";
import { AnthropicAdapter } from "./llm-anthropic";
import {
  fakeServer,
  isolateEnv,
  reply,
  type FakeServer,
} from "../test/llm/conformance-helpers";

let restoreEnv: () => void;
const servers: FakeServer[] = [];
beforeEach(() => {
  restoreEnv = isolateEnv();
});
afterEach(async () => {
  restoreEnv();
  await Promise.all(servers.splice(0).map((s) => s.close()));
});

async function server(handler: Parameters<typeof fakeServer>[0]): Promise<FakeServer> {
  const s = await fakeServer(handler);
  servers.push(s);
  return s;
}

const OK = JSON.stringify({
  type: "message",
  role: "assistant",
  content: [{ type: "text", text: "ok" }],
  stop_reason: "end_turn",
  usage: { input_tokens: 1, output_tokens: 1 },
});
const HI = [{ role: "user", content: "Hi" }];

describe("anthropic registry", () => {
  it("resolves anthropic:// with ANTHROPIC_API_KEY to a trusted base", async () => {
    const s = await server((_h, res) => reply(res, "application/json", OK));
    process.env["ANTHROPIC_API_KEY"] = "env-key";
    process.env["ANTHROPIC_BASE_URL"] = s.url;
    const r = await resolve("anthropic://claude-x").complete!({ messages: HI });
    expect(r.content).toBe("ok");
    expect(s.hits[0]!.url).toBe("/v1/messages");
    expect(s.hits[0]!.headers["x-api-key"]).toBe("env-key");
    expect(JSON.parse(s.hits[0]!.body).model).toBe("claude-x");
  });

  it("keeps an env key off an endpoint only the URI names", () => {
    process.env["ANTHROPIC_API_KEY"] = "env-key";
    expect(() => resolve("anthropic://m?base_url=https://evil.example.test")).toThrow(
      KeyEndpointError
    );
    expect(() =>
      resolve("anthropic://m?base_url=https://gw.example.test&api_key=uri-key")
    ).not.toThrow();
  });

  it("requires a key and a parseable base URL", () => {
    expect(() => resolve("anthropic://m")).toThrow(/API key required/);
    expect(
      () =>
        new AnthropicAdapter({
          uri: { scheme: "anthropic", model: "m" },
          provider: { model: "m", apiKey: "k", baseURL: "::nope?api_key=secret-value" },
          fallbacks: [],
        })
    ).toThrow(LLMError);
    try {
      new AnthropicAdapter({
        uri: { scheme: "anthropic", model: "m" },
        provider: { model: "m", apiKey: "k", baseURL: "::nope?api_key=secret-value" },
        fallbacks: [],
      });
    } catch (e) {
      expect((e as Error).message).not.toContain("secret-value");
    }
  });

  it("refuses the key over plain http to a remote host", () => {
    process.env["ANTHROPIC_API_KEY"] = "env-key";
    process.env["ANTHROPIC_BASE_URL"] = "http://gw.example.test";
    expect(() => resolve("anthropic://m")).toThrow(KeyEndpointError);
  });

  it("refuses an unknown tool_names mode at construction", () => {
    process.env["ANTHROPIC_API_KEY"] = "env-key";
    expect(() => resolve("anthropic://m?tool_names=loose")).toThrow(/tool_names/);
  });
});

describe("anthropic tool round", () => {
  it("streams thinking, then replays the outcome in the next turn", async () => {
    const events = [
      { type: "message_start", message: { content: [], usage: { input_tokens: 3, output_tokens: 0 } } },
      { type: "content_block_start", index: 0, content_block: { type: "thinking", thinking: "", signature: "" } },
      { type: "content_block_delta", index: 0, delta: { type: "thinking_delta", thinking: "hmm" } },
      { type: "content_block_delta", index: 0, delta: { type: "signature_delta", signature: "sig" } },
      { type: "content_block_stop", index: 0 },
      { type: "content_block_start", index: 1, content_block: { type: "tool_use", id: "t1", name: "pane_open", input: {} } },
      { type: "content_block_delta", index: 1, delta: { type: "input_json_delta", partial_json: '{"id":' } },
      { type: "content_block_delta", index: 1, delta: { type: "input_json_delta", partial_json: "7}" } },
      { type: "content_block_stop", index: 1 },
      { type: "message_delta", delta: { stop_reason: "tool_use" }, usage: { output_tokens: 9 } },
      { type: "message_stop" },
    ];
    const s = await server((_h, res, n) =>
      n === 0
        ? reply(res, "text/event-stream", events.map((e) => `data: ${JSON.stringify(e)}\n\n`).join(""))
        : reply(res, "application/json", OK)
    );
    process.env["ANTHROPIC_API_KEY"] = "k";
    process.env["ANTHROPIC_BASE_URL"] = s.url;
    const client = createLLM("anthropic://m");
    const tools = [{ name: "pane.open", description: "", parameters: undefined }];
    const req = { messages: HI, maxTokens: 4096, thinking: { budgetTokens: 2048 } };
    const st = client.streamWithTools(req, tools);
    const toks: Token[] = [];
    for await (const t of st) toks.push(t);
    expect(toks).toEqual([
      { content: "", thinking: "hmm", done: false },
      { content: "", done: true },
    ]);
    const out = st.outcome()!;
    expect(out.toolCalls).toEqual([{ id: "t1", name: "pane.open", arguments: { id: 7 } }]);
    expect(out.thinking).toEqual([{ thinking: "hmm", signature: "sig", redacted: "" }]);

    await client.callWithTools(
      {
        ...req,
        messages: [
          ...HI,
          { role: "assistant", content: out.content, toolCalls: out.toolCalls, thinking: out.thinking },
          { role: "tool", content: "opened", toolCallId: "t1" },
        ],
      },
      tools
    );
    const body = JSON.parse(s.hits[1]!.body);
    expect(body.messages[1].content).toEqual([
      { type: "thinking", thinking: "hmm", signature: "sig" },
      { type: "tool_use", id: "t1", name: "pane_open", input: { id: 7 } },
    ]);
    expect(body.tools[0].name).toBe("pane_open");
  });
});
