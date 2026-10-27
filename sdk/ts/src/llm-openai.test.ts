/**
 * OpenAI adapter against loopback fake servers: wire shape, tool calls
 * and names both ways, streaming with tools, and the transport guards
 * it shares with the anthropic adapter (size caps, truncation, retry
 * bounds, redirects, credential scrubbing and the endpoint gate).
 */

import { afterEach, beforeEach, describe, expect, it } from "vitest";

import {
  APIError,
  AuthError,
  HTTPStatusError,
  KeyEndpointError,
  ModelError,
  RateLimitError,
  ResponseTooLargeError,
  StreamTruncatedError,
  ToolNameError,
  createLLM,
  isFallbackable,
  resolve,
  type Message,
  type ResolvedConfig,
  type Token,
  type ToolDef,
} from "./llm";
import { OpenAIAdapter, apiRootV1 } from "./llm-openai";
import {
  fakeServer,
  isolateEnv,
  reply,
  type FakeServer,
} from "../test/llm/conformance-helpers";

const KEY = "sk-must-not-leak";

let restoreEnv: () => void;
const servers: FakeServer[] = [];
beforeEach(() => {
  restoreEnv = isolateEnv([
    "OPENAI_API_KEY", "OPENROUTER_API_KEY", "LMSTUDIO_API_KEY", "LLM_API_KEY",
    "LLM_BASE_URL", "LLM_PROVIDER", "LLM_FALLBACK", "ROUTELLM_BASE_URL",
  ]);
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

function cfg(base: string, params?: Record<string, string>, key = KEY): ResolvedConfig {
  return {
    uri: { scheme: "openai", model: "gpt-test" },
    provider: { apiKey: key, baseURL: base, model: "gpt-test", params },
    fallbacks: [],
  };
}

const completion = (message: Record<string, unknown>, finish = "stop") =>
  JSON.stringify({
    id: "c1",
    object: "chat.completion",
    choices: [{ index: 0, message: { role: "assistant", ...message }, finish_reason: finish }],
    usage: { prompt_tokens: 3, completion_tokens: 2, total_tokens: 5 },
  });

const sse = (...events: (object | string)[]) =>
  events.map((e) => `data: ${typeof e === "string" ? e : JSON.stringify(e)}\n\n`).join("");

const chunk = (delta: Record<string, unknown>, finish: string | null = null) => ({
  choices: [{ index: 0, delta, finish_reason: finish }],
});

async function drain(s: AsyncIterable<Token>) {
  const tokens: string[] = [];
  let error: unknown;
  try {
    for await (const t of s) {
      if (t.done) break;
      tokens.push(t.content);
    }
  } catch (e) {
    error = e;
  }
  return { tokens, error };
}

const HI: Message[] = [{ role: "user", content: "Hi" }];
const tools: ToolDef[] = [
  { name: "pane.open", description: "Open a pane", parameters: '{"type":"object"}' },
  { name: "files.read", description: "", parameters: { type: "object", properties: {} } },
];

describe("openai adapter: requests", () => {
  it("posts the chat body to /chat/completions with a bearer key", async () => {
    const s = await server((_h, res) => reply(res, "application/json", completion({ content: "hey" })));
    const r = await new OpenAIAdapter(cfg(s.url)).complete({
      messages: [{ role: "system", content: "be brief" }, ...HI],
      temperature: 0,
      maxTokens: 64,
      stopSequences: ["END"],
    });
    expect(r).toEqual({
      content: "hey",
      role: "assistant",
      finishReason: "stop",
      usage: { promptTokens: 3, completionTokens: 2, totalTokens: 5 },
    });
    const hit = s.hits[0]!;
    expect(hit.url).toBe("/chat/completions");
    expect(hit.headers["authorization"]).toBe(`Bearer ${KEY}`);
    expect(JSON.parse(hit.body)).toEqual({
      model: "gpt-test",
      messages: [
        { role: "system", content: "be brief" },
        { role: "user", content: "Hi" },
      ],
      temperature: 0,
      max_tokens: 64,
      stop: ["END"],
    });
  });

  it("sends no Authorization header for a blank key", async () => {
    const s = await server((_h, res) => reply(res, "application/json", completion({ content: "" })));
    await new OpenAIAdapter(cfg(s.url, undefined, "  ")).complete({ messages: HI });
    expect(s.hits[0]!.headers["authorization"]).toBeUndefined();
  });

  it("appends /v1 for a host-form URI and lmstudio, not for base_url", () => {
    expect(apiRootV1("http://h:1")).toBe("http://h:1/v1");
    expect(apiRootV1("http://h:1/v1/")).toBe("http://h:1/v1/");
    const host = new OpenAIAdapter({
      uri: { scheme: "openai", model: "m", host: "127.0.0.1:8000" },
      provider: { model: "m", baseURL: "http://127.0.0.1:8000" },
      fallbacks: [],
    });
    expect(host.baseURL).toBe("http://127.0.0.1:8000/v1");
    const named = new OpenAIAdapter({
      uri: { scheme: "openai", model: "m", params: { base_url: "https://gw.test/x" } },
      provider: { model: "m", baseURL: "https://gw.test/x" },
      fallbacks: [],
    });
    expect(named.baseURL).toBe("https://gw.test/x");
    const lm = new OpenAIAdapter({
      uri: { scheme: "lmstudio", model: "m" },
      provider: { model: "m" },
      fallbacks: [],
    });
    expect(lm.baseURL).toBe("http://localhost:1234/v1");
  });
});

describe("openai adapter: tool calling", () => {
  it("sends sanitized tool names and returns calls under the original", async () => {
    const s = await server((_h, res) =>
      reply(
        res,
        "application/json",
        completion(
          {
            content: null,
            tool_calls: [
              { id: "call_1", type: "function", function: { name: "pane_open", arguments: '{"id":7}' } },
            ],
          },
          "tool_calls"
        )
      )
    );
    const r = await new OpenAIAdapter(cfg(s.url)).callWithTools(
      {
        messages: [
          ...HI,
          {
            role: "assistant",
            content: "",
            toolCalls: [{ id: "call_0", name: "files.read", arguments: "{}" }],
          },
          { role: "tool", content: "ok", toolCallId: "call_0" },
        ],
      },
      tools
    );
    expect(r.toolCalls).toEqual([{ id: "call_1", name: "pane.open", arguments: { id: 7 } }]);
    expect(r.finishReason).toBe("tool_calls");
    const body = JSON.parse(s.hits[0]!.body);
    expect(body.tools).toEqual([
      {
        type: "function",
        function: { name: "pane_open", description: "Open a pane", parameters: { type: "object" } },
      },
      { type: "function", function: { name: "files_read", parameters: { type: "object", properties: {} } } },
    ]);
    expect(body.messages[1].tool_calls[0].function.name).toBe("files_read");
    expect(body.messages[2]).toEqual({ role: "tool", content: "ok", tool_call_id: "call_0" });
  });

  it("strict mode rejects an invalid name before sending", async () => {
    const s = await server((_h, res) => reply(res, "application/json", completion({})));
    const err = await new OpenAIAdapter(cfg(s.url, { tool_names: "strict" }))
      .callWithTools({ messages: HI }, tools)
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ToolNameError);
    expect((err as ToolNameError).reason).toBe("invalid");
    expect(isFallbackable(err as Error)).toBe(false);
    expect(s.hits).toHaveLength(0);
  });

  it("a collision fails a stream before sending", async () => {
    const s = await server((_h, res) => reply(res, "text/event-stream", sse("[DONE]")));
    const r = await drain(
      new OpenAIAdapter(cfg(s.url)).streamWithTools({ messages: HI }, [
        { name: "a.b", description: "", parameters: undefined },
        { name: "a:b", description: "", parameters: undefined },
      ])
    );
    expect(r.error).toBeInstanceOf(ToolNameError);
    expect((r.error as ToolNameError).reason).toBe("collision");
    expect(s.hits).toHaveLength(0);
  });

  it("streams text, then folds tool-call deltas into the outcome", async () => {
    const s = await server((_h, res) =>
      reply(
        res,
        "text/event-stream",
        sse(
          chunk({ role: "assistant", content: "Let me " }),
          chunk({ content: "check." }),
          chunk({
            tool_calls: [
              { index: 0, id: "call_1", type: "function", function: { name: "pane_open", arguments: "" } },
            ],
          }),
          chunk({ tool_calls: [{ index: 0, function: { arguments: '{"id"' } }] }),
          chunk({ tool_calls: [{ index: 0, function: { arguments: ":7}" } }] }),
          chunk({}, "tool_calls"),
          { choices: [], usage: { prompt_tokens: 4, completion_tokens: 6, total_tokens: 10 } },
          "[DONE]"
        )
      )
    );
    const st = new OpenAIAdapter(cfg(s.url)).streamWithTools({ messages: HI }, tools);
    expect(st.outcome()).toBeUndefined();
    const r = await drain(st);
    expect(r.error).toBeUndefined();
    expect(r.tokens).toEqual(["Let me ", "check."]);
    expect(st.outcome()).toEqual({
      content: "Let me check.",
      thinking: [],
      toolCalls: [{ id: "call_1", name: "pane.open", arguments: { id: 7 } }],
      finishReason: "tool_calls",
      usage: { promptTokens: 4, completionTokens: 6, totalTokens: 10 },
    });
    expect(JSON.parse(s.hits[0]!.body).stream).toBe(true);
  });

  it("maps streamed history names forward", async () => {
    const s = await server((_h, res) => reply(res, "text/event-stream", sse("[DONE]")));
    await drain(
      new OpenAIAdapter(cfg(s.url)).stream({
        messages: [
          { role: "assistant", content: "", toolCalls: [{ id: "c", name: "ns/tool", arguments: "{}" }] },
          { role: "tool", content: "x", toolCallId: "c" },
        ],
      })
    );
    expect(JSON.parse(s.hits[0]!.body).messages[0].tool_calls[0].function.name).toBe("ns_tool");
  });
});

describe("openai adapter: stream termination", () => {
  it("is truncated when the body ends before any finish or [DONE]", async () => {
    const s = await server((_h, res) =>
      reply(res, "text/event-stream", sse(chunk({ content: "parti" })))
    );
    const r = await drain(new OpenAIAdapter(cfg(s.url)).stream({ messages: HI }));
    expect(r.tokens).toEqual(["parti"]);
    expect(r.error).toBeInstanceOf(StreamTruncatedError);
    expect(isFallbackable(r.error as Error)).toBe(false);
  });

  it("completes when a choice finished though [DONE] never came", async () => {
    const s = await server((_h, res) =>
      reply(res, "text/event-stream", sse(chunk({ content: "a" }), chunk({}, "stop")))
    );
    const st = new OpenAIAdapter(cfg(s.url)).stream({ messages: HI });
    const r = await drain(st);
    expect(r.error).toBeUndefined();
    expect(st.outcome()?.finishReason).toBe("stop");
  });

  it("fails on an error chunk mid-stream", async () => {
    const s = await server((_h, res) =>
      reply(
        res,
        "text/event-stream",
        sse(chunk({ content: "a" }), { error: { type: "server_error", message: `boom ${KEY}` } })
      )
    );
    const r = await drain(new OpenAIAdapter(cfg(s.url)).stream({ messages: HI }));
    expect(r.error).toBeInstanceOf(APIError);
    expect((r.error as Error).message).not.toContain(KEY);
  });
});

describe("openai adapter: transport guards", () => {
  it("caps a body at max_body_bytes without retrying", async () => {
    const s = await server((_h, res) =>
      reply(res, "application/json", completion({ content: "x".repeat(4096) }))
    );
    const err = await new OpenAIAdapter(cfg(s.url, { max_body_bytes: "1024" }))
      .complete({ messages: HI })
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ResponseTooLargeError);
    expect((err as ResponseTooLargeError).what).toBe("body");
    expect(s.hits).toHaveLength(1);
  });

  it("caps one SSE event at max_event_bytes", async () => {
    const s = await server((_h, res) =>
      reply(res, "text/event-stream", sse(chunk({ content: "y".repeat(4096) }), "[DONE]"))
    );
    const r = await drain(
      new OpenAIAdapter(cfg(s.url, { max_event_bytes: "1024" })).stream({ messages: HI })
    );
    expect(r.error).toBeInstanceOf(ResponseTooLargeError);
    expect((r.error as ResponseTooLargeError).what).toBe("event");
  });

  it("caps an event that never ends", async () => {
    const s = await server((_h, res) =>
      reply(res, "text/event-stream", "data: " + "z".repeat(4096))
    );
    const r = await drain(
      new OpenAIAdapter(cfg(s.url, { max_event_bytes: "1024" })).stream({ messages: HI })
    );
    expect(r.error).toBeInstanceOf(ResponseTooLargeError);
  });

  it("refuses a limit that is not a positive byte count", () => {
    for (const v of ["0", "-1", "1.5", "lots"]) {
      expect(() => new OpenAIAdapter(cfg("http://127.0.0.1:9", { max_body_bytes: v }))).toThrow(
        /max_body_bytes must be a positive byte count/
      );
    }
  });

  it("honors a retry-after within 60s and retries 5xx", { timeout: 10_000 }, async () => {
    const s = await server((_h, res, n) => {
      if (n === 0) {
        res.writeHead(503, { "retry-after-ms": "5", "content-type": "application/json" });
        res.end('{"error":{"message":"busy"}}');
        return;
      }
      reply(res, "application/json", completion({ content: "ok" }));
    });
    const r = await new OpenAIAdapter(cfg(s.url)).complete({ messages: HI });
    expect(r.content).toBe("ok");
    expect(s.hits).toHaveLength(2);
  });

  it("falls back to backoff for a retry-after over 60s", { timeout: 10_000 }, async () => {
    const s = await server((_h, res, n) => {
      if (n === 0) {
        res.writeHead(429, { "retry-after": "3600", "content-type": "application/json" });
        res.end("{}");
        return;
      }
      reply(res, "application/json", completion({ content: "ok" }));
    });
    const start = Date.now();
    await new OpenAIAdapter(cfg(s.url)).complete({ messages: HI });
    expect(Date.now() - start).toBeLessThan(5_000);
    expect(s.hits).toHaveLength(2);
  });

  it("classifies errors and scrubs the key from them", { timeout: 10_000 }, async () => {
    const cases: [number, new (...a: never[]) => Error, boolean][] = [
      [401, AuthError, false],
      [404, ModelError, false],
      [400, APIError, false],
      [429, RateLimitError, true],
      [500, HTTPStatusError, true],
    ];
    for (const [status, cls, fallbackable] of cases) {
      const s = await server((_h, res) => {
        res.writeHead(status, { "content-type": "application/json", "x-should-retry": "false" });
        res.end(JSON.stringify({ error: { type: "e", message: `echo ${KEY}` } }));
      });
      const err = (await new OpenAIAdapter(cfg(s.url))
        .complete({ messages: HI })
        .catch((e: unknown) => e)) as Error;
      expect(err, String(status)).toBeInstanceOf(cls);
      expect(isFallbackable(err)).toBe(fallbackable);
      expect(err.message).not.toContain(KEY);
      expect(String((err as { cause?: Error }).cause?.message)).not.toContain(KEY);
    }
  });

  it("scrubs a key cut by the error-text limit", async () => {
    // The key straddles the 2048-char cut: scrubbing after the cut
    // would miss its head.
    const s = await server((_h, res) => {
      res.writeHead(500, { "content-type": "text/plain", "x-should-retry": "false" });
      res.end("x".repeat(2040) + KEY + " tail");
    });
    const err = (await new OpenAIAdapter(cfg(s.url))
      .complete({ messages: HI })
      .catch((e: unknown) => e)) as Error;
    expect(err).toBeInstanceOf(HTTPStatusError);
    expect(err.message).not.toContain(KEY.slice(0, 6));
  });

  it("does not follow a redirect", async () => {
    const target = await server((_h, res) => reply(res, "application/json", completion({})));
    const s = await server((_h, res) => {
      res.writeHead(307, { location: `${target.url}/chat/completions` });
      res.end();
    });
    const err = await new OpenAIAdapter(cfg(s.url))
      .complete({ messages: HI })
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(HTTPStatusError);
    expect((err as HTTPStatusError).statusCode).toBe(307);
    expect(s.hits).toHaveLength(1);
    expect(target.hits, "the key never reaches the redirect target").toHaveLength(0);
  });

  it("refuses a key over plain http to a remote host", () => {
    expect(() => new OpenAIAdapter(cfg("http://gw.example.test/v1"))).toThrow(KeyEndpointError);
    expect(() => new OpenAIAdapter(cfg("https://user:pw@gw.example.test/v1"))).toThrow(
      KeyEndpointError
    );
    expect(() => new OpenAIAdapter(cfg("http://127.0.0.1:9/v1"))).not.toThrow();
    // No key, nothing to guard.
    expect(() => new OpenAIAdapter(cfg("http://gw.example.test/v1", undefined, ""))).not.toThrow();
  });
});

describe("openai registry", () => {
  it("resolves every scheme and reads <SCHEME>_API_KEY", async () => {
    const s = await server((_h, res) => reply(res, "application/json", completion({ content: "ok" })));
    process.env["OPENROUTER_API_KEY"] = "or-key";
    process.env["LLM_BASE_URL"] = s.url;
    await resolve("openrouter://openai/gpt-4.1-nano").complete!({ messages: HI });
    expect(s.hits[0]!.headers["authorization"]).toBe("Bearer or-key");
    expect(JSON.parse(s.hits[0]!.body).model).toBe("openai/gpt-4.1-nano");
    for (const scheme of ["openai", "xai", "groq", "together", "fireworks", "deepseek", "mistral"]) {
      expect(() => resolve(`${scheme}://m`)).not.toThrow();
    }
  });

  it("keeps an env key off an endpoint only the URI names", () => {
    process.env["OPENAI_API_KEY"] = "env-key";
    expect(() => resolve("openai://m?base_url=https://evil.example.test/v1")).toThrow(
      KeyEndpointError
    );
    // The URI's own key is the caller's choice for an https host.
    expect(() =>
      resolve("openai://m?base_url=https://gw.example.test/v1&api_key=uri-key")
    ).not.toThrow();
    // Loopback is trusted.
    expect(() => resolve("openai://127.0.0.1:8000/m")).not.toThrow();
  });

  it("lmstudio sends no env key", async () => {
    const s = await server((_h, res) => reply(res, "application/json", completion({ content: "ok" })));
    process.env["LMSTUDIO_API_KEY"] = "lm-key";
    process.env["LLM_BASE_URL"] = s.url;
    await resolve("lmstudio://m").complete!({ messages: HI });
    expect(s.hits[0]!.headers["authorization"]).toBeUndefined();
  });

  it("client streamWithTools falls back before the first token", async () => {
    const bad = await server((_h, res) => {
      res.writeHead(500, { "content-type": "application/json", "x-should-retry": "false" });
      res.end("{}");
    });
    const good = await server((_h, res) =>
      reply(
        res,
        "text/event-stream",
        sse(
          chunk({
            tool_calls: [{ index: 0, id: "c1", function: { name: "pane_open", arguments: "{}" } }],
          }),
          chunk({}, "tool_calls"),
          "[DONE]"
        )
      )
    );
    const client = createLLM(`openai://m?base_url=${bad.url}`, {
      fallback: [`openai://m?base_url=${good.url}`],
    });
    expect(client.capabilities()).toContain("streamWithTools");
    const st = client.streamWithTools({ messages: HI }, tools);
    const r = await drain(st);
    expect(r.error).toBeUndefined();
    expect(st.outcome()?.toolCalls).toEqual([{ id: "c1", name: "pane.open", arguments: {} }]);
    expect(bad.hits).toHaveLength(1);
  });
});
