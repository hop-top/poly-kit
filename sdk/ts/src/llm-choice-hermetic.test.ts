/**
 * tool_choice on the openai and routellm adapters, the 401/403 split,
 * and hermetic resolution (no environment read by the config, the
 * registry factories or the adapters). The anthropic side of each runs
 * in the llm-v1 conformance runner.
 */

import { afterEach, beforeEach, describe, expect, it } from "vitest";

import "./llm-anthropic";
import {
  AuthError,
  KeyEndpointError,
  LLMError,
  PermissionDeniedError,
  checkToolChoice,
  createLLM,
  isFallbackable,
  loadConfig,
  register,
  resolve,
  type Request,
  type ResolvedConfig,
  type ToolChoice,
  type ToolDef,
} from "./llm";
import { withResolvedKey } from "./llm-keys";
import { OpenAIAdapter } from "./llm-openai";
import { RouteLLMAdapter, parseRouterConfig } from "./routellm";
import { fakeServer, isolateEnv, reply, type FakeServer } from "../test/llm/conformance-helpers";

const ENV = [
  "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_WORKSPACE_ID",
  "OPENAI_API_KEY", "LLM_API_KEY", "LLM_BASE_URL", "LLM_PROVIDER", "LLM_FALLBACK",
  "ROUTELLM_BASE_URL", "ROUTELLM_STRONG_MODEL", "ROUTELLM_WEAK_MODEL", "ROUTELLM_ROUTERS",
];

let restoreEnv: () => void;
const servers: FakeServer[] = [];
beforeEach(() => {
  restoreEnv = isolateEnv(ENV);
});
afterEach(async () => {
  restoreEnv();
  await Promise.all(servers.splice(0).map((s) => s.close()));
});

const COMPLETION = JSON.stringify({
  id: "c1",
  object: "chat.completion",
  choices: [{ index: 0, message: { role: "assistant", content: "ok" }, finish_reason: "stop" }],
  usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 },
});
const ANTHROPIC_OK = JSON.stringify({
  id: "msg_1", type: "message", role: "assistant", model: "m",
  content: [{ type: "text", text: "ok" }], stop_reason: "end_turn",
  usage: { input_tokens: 1, output_tokens: 1 },
});

async function server(handler: Parameters<typeof fakeServer>[0]): Promise<FakeServer> {
  const s = await fakeServer(handler);
  servers.push(s);
  return s;
}

const okServer = (body = COMPLETION) => server((_h, res) => reply(res, "application/json", body));

function openaiCfg(base: string): ResolvedConfig {
  return {
    uri: { scheme: "openai", model: "gpt-test" },
    provider: { apiKey: "k", baseURL: base, model: "gpt-test" },
    fallbacks: [],
  };
}

const TOOLS: ToolDef[] = [
  { name: "get_weather", description: "", parameters: undefined },
  { name: "pane.open", description: "", parameters: undefined },
];
const hi = (toolChoice?: ToolChoice): Request => ({
  messages: [{ role: "user", content: "Hi" }],
  toolChoice,
});

/** Intercepts fetch; returns what was sent and a restore function. */
function captureFetch(body: string) {
  const sent: { url: string; headers: Headers }[] = [];
  const orig = globalThis.fetch;
  globalThis.fetch = (async (u: unknown, init?: RequestInit) => {
    sent.push({ url: String(u), headers: new Headers(init?.headers) });
    return new globalThis.Response(body, {
      status: 200,
      headers: { "content-type": "application/json" },
    });
  }) as typeof fetch;
  return { sent, restore: () => (globalThis.fetch = orig) };
}

describe("checkToolChoice", () => {
  it("accepts unset and each valid mode", () => {
    expect(() => checkToolChoice(undefined, [])).not.toThrow();
    for (const mode of ["auto", "any", "none"] as const) {
      expect(() => checkToolChoice({ mode }, TOOLS)).not.toThrow();
    }
    expect(() => checkToolChoice({ mode: "tool", name: "pane.open" }, TOOLS)).not.toThrow();
  });

  it("rejects in Go's order with Go's messages", () => {
    const bad = (c: ToolChoice, tools: ToolDef[] = TOOLS) => () => checkToolChoice(c, tools);
    expect(bad({ mode: "required" as ToolChoice["mode"] }, [])).toThrow(
      'unknown tool_choice mode "required"'
    );
    expect(bad({ mode: "auto" }, [])).toThrow('tool_choice "auto" needs tools');
    expect(bad({ mode: "any", name: "get_weather" })).toThrow('tool_choice "any" takes no tool name');
    expect(bad({ mode: "tool" })).toThrow('tool_choice "tool" needs a tool name');
    expect(bad({ mode: "tool", name: "nope" })).toThrow('tool_choice names unknown tool "nope"');
    expect(bad({ mode: "auto" }, [])).toThrow(LLMError);
  });
});

describe("openai tool_choice", () => {
  const cases: [string, ToolChoice | undefined, unknown][] = [
    ["unset sends none", undefined, undefined],
    ["auto", { mode: "auto" }, "auto"],
    ["any is required", { mode: "any" }, "required"],
    ["none", { mode: "none" }, "none"],
    ["named, by sent name", { mode: "tool", name: "pane.open" },
      { type: "function", function: { name: "pane_open" } }],
  ];
  for (const [name, choice, want] of cases) {
    it(name, async () => {
      const s = await okServer();
      await new OpenAIAdapter(openaiCfg(s.url)).callWithTools(hi(choice), TOOLS);
      const body = JSON.parse(s.hits[0]!.body) as Record<string, unknown>;
      if (want === undefined) expect("tool_choice" in body).toBe(false);
      else expect(body["tool_choice"]).toEqual(want);
    });
  }

  it("rejects before sending, on every call kind", async () => {
    const s = await okServer();
    const a = new OpenAIAdapter(openaiCfg(s.url));
    await expect(a.complete(hi({ mode: "any" }))).rejects.toThrow(
      'openai: tool_choice "any" needs tools'
    );
    const it1 = a.stream(hi({ mode: "any" }))[Symbol.asyncIterator]();
    await expect(it1.next()).rejects.toThrow('openai: tool_choice "any" needs tools');
    await expect(a.callWithTools(hi({ mode: "tool", name: "nope" }), TOOLS)).rejects.toThrow(
      'openai: tool_choice names unknown tool "nope"'
    );
    const it2 = a.streamWithTools(hi({ mode: "tool" }), TOOLS)[Symbol.asyncIterator]();
    await expect(it2.next()).rejects.toThrow('openai: tool_choice "tool" needs a tool name');
    expect(s.hits).toHaveLength(0);
  });

  it("routellm passes it through, errors under its label", async () => {
    const s = await okServer();
    const r = new RouteLLMAdapter({
      uri: { scheme: "routellm", model: "mf:0.7" },
      provider: { model: "mf:0.7", baseURL: s.url },
      fallbacks: [],
    });
    await r.callWithTools(hi({ mode: "any" }), TOOLS);
    expect((JSON.parse(s.hits[0]!.body) as Record<string, unknown>)["tool_choice"]).toBe("required");
    await expect(r.complete(hi({ mode: "auto" }))).rejects.toThrow(
      'routellm: tool_choice "auto" needs tools'
    );
    expect(s.hits).toHaveLength(1);
  });
});

describe("401/403 split", () => {
  for (const [status, denied] of [[403, true], [401, false]] as const) {
    it(`openai ${status}`, async () => {
      const s = await server((_h, res) =>
        reply(res, "application/json", '{"error":{"message":"no","type":"x"}}', status)
      );
      const err = await new OpenAIAdapter(openaiCfg(s.url))
        .complete(hi())
        .then(() => undefined, (e: unknown) => e);
      expect(err instanceof PermissionDeniedError).toBe(denied);
      expect(err, "every 401 and 403 is an auth error").toBeInstanceOf(AuthError);
      expect(isFallbackable(err as Error)).toBe(false);
      expect((err as AuthError).provider).toBe("openai");
    });
  }

  it("a denial names itself and keeps the cause", () => {
    const cause = new Error("no access");
    const e = new PermissionDeniedError("anthropic", cause);
    expect(e.message).toBe("permission denied for provider: anthropic: no access");
    expect(e.name).toBe("PermissionDeniedError");
    expect(e.cause).toBe(cause);
    expect(e).toBeInstanceOf(AuthError);
    expect(e).toBeInstanceOf(LLMError);
  });
});

describe("hermetic", () => {
  it("loadConfig reads no LLM_* variable", () => {
    process.env["LLM_PROVIDER"] = "openai://ambient";
    process.env["LLM_FALLBACK"] = "openai://fb";
    process.env["LLM_API_KEY"] = "env-key";
    process.env["LLM_BASE_URL"] = "https://gw.corp.test";
    const cfg = loadConfig("anthropic://m", { hermetic: true });
    expect(cfg.hermetic).toBe(true);
    expect(cfg.fallbacks).toEqual([]);
    expect(cfg.provider.apiKey).toBeUndefined();
    expect(cfg.provider.baseURL).toBeUndefined();
    expect(() => loadConfig("", { hermetic: true })).toThrow(/LLM_PROVIDER is not read/);
    // Not hermetic, every one of them counts.
    const amb = loadConfig("");
    expect(amb.uri.model).toBe("ambient");
    expect(amb.fallbacks).toEqual(["openai://fb"]);
    expect(amb.provider.apiKey).toBe("env-key");
  });

  it("anthropic via resolve: only the URI's key, default host, no env header", async () => {
    Object.assign(process.env, {
      ANTHROPIC_API_KEY: "env-key",
      ANTHROPIC_AUTH_TOKEN: "env-token",
      ANTHROPIC_WORKSPACE_ID: "ws-1",
      ANTHROPIC_BASE_URL: "https://gw.corp.test",
      LLM_BASE_URL: "https://gw.corp.test",
    });
    const f = captureFetch(ANTHROPIC_OK);
    try {
      await resolve("anthropic://m?api_key=uri-key", { hermetic: true }).complete!(hi());
    } finally {
      f.restore();
    }
    expect(new URL(f.sent[0]!.url).host).toBe("api.anthropic.com");
    expect(f.sent[0]!.headers.get("x-api-key")).toBe("uri-key");
    expect(f.sent[0]!.headers.get("authorization")).toBeNull();
    expect(f.sent[0]!.headers.get("anthropic-workspace-id")).toBeNull();
  });

  it("anthropic via resolve: no env key to fall back on", () => {
    process.env["ANTHROPIC_API_KEY"] = "env-key";
    process.env["LLM_API_KEY"] = "env-key";
    expect(() => resolve("anthropic://m", { hermetic: true })).toThrow(
      "anthropic: API key required"
    );
    expect(() => resolve("anthropic://m")).not.toThrow();
  });

  it("openai via createLLM: no <SCHEME>_API_KEY, no LLM_BASE_URL", async () => {
    process.env["OPENAI_API_KEY"] = "env-key";
    process.env["LLM_BASE_URL"] = "https://gw.corp.test";
    const f = captureFetch(COMPLETION);
    try {
      await createLLM("openai://gpt-test", { hermetic: true }).complete(hi());
    } finally {
      f.restore();
    }
    expect(new URL(f.sent[0]!.url).host).toBe("api.openai.com");
    expect(f.sent[0]!.headers.get("authorization")).toBeNull();
  });

  it("the key gate's trust check reads no LLM_BASE_URL", () => {
    // A key not from the URI, bound for an endpoint only the URI names:
    // trusted only through LLM_BASE_URL, which hermetic does not read.
    process.env["LLM_BASE_URL"] = "https://gw.corp.test";
    const cfg: ResolvedConfig = {
      uri: { scheme: "openai", model: "m", params: { base_url: "https://gw.corp.test" } },
      provider: { apiKey: "caller-key", baseURL: "https://gw.corp.test", model: "m" },
      fallbacks: [],
    };
    expect(() => withResolvedKey({ ...cfg, hermetic: true }, undefined, [])).toThrow(
      KeyEndpointError
    );
    expect(() => withResolvedKey(cfg, undefined, [])).not.toThrow();
  });

  it("routellm reads no ROUTELLM_* variable", () => {
    Object.assign(process.env, {
      ROUTELLM_BASE_URL: "http://env.test:9",
      ROUTELLM_STRONG_MODEL: "s",
      ROUTELLM_WEAK_MODEL: "w",
      ROUTELLM_ROUTERS: "mf",
    });
    const extras = { routellm: { base_url: "http://cfg.test:7" } };
    expect(parseRouterConfig(extras, { hermetic: true })).toMatchObject({
      baseUrl: "http://cfg.test:7", strongModel: "", weakModel: "", routers: [],
    });
    expect(parseRouterConfig(extras).baseUrl).toBe("http://env.test:9");
    const r = new RouteLLMAdapter({
      uri: { scheme: "routellm", model: "mf:0.5" },
      provider: { model: "mf:0.5", extras },
      fallbacks: [],
      hermetic: true,
    });
    expect(r.config.baseUrl).toBe("http://cfg.test:7");
  });
});

describe("client onResponse after a tool call", () => {
  it("gets the turn's usage, stop reason and thinking", async () => {
    const usage = { promptTokens: 3, completionTokens: 4, totalTokens: 7 };
    const thinking = [{ thinking: "hmm", signature: "sig", redacted: "" }];
    register("mock-onresponse", () => ({
      callWithTools: async () => ({
        content: "c", toolCalls: [], usage, finishReason: "tool_use", thinking,
      }),
      close() {},
    }));
    let got: unknown;
    await createLLM("mock-onresponse://m", {
      onResponse: (r) => {
        got = r;
      },
    }).callWithTools(hi(), TOOLS);
    expect(got).toEqual({
      content: "c", role: "assistant", usage, finishReason: "tool_use", thinking,
    });
  });
});
