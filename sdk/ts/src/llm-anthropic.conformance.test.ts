// Runs contracts/llm-v1/vectors.json against this SDK: every
// anthropic_* section through AnthropicAdapter and loopback fake
// servers, credential_headers, redact_uri and usage_cost. Go is
// canonical; a failure means this SDK drifted, not that a vector
// should change. tool_names runs in llm-toolnames.conformance.test.ts.
//
// Sections this SDK does not run are listed in NOT_RUN with the reason;
// a section in neither list fails the run, so none is skipped silently.

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
  inlineSource,
  isFallbackable,
  redactURI,
  usageCost,
  type Message,
  type Request,
  type ResolvedConfig,
  type ToolCall,
  type ToolDef,
  type ToolStream,
} from "./llm";
import { AnthropicAdapter } from "./llm-anthropic";
import {
  fakeServer,
  isolateEnv,
  loadVectors,
  reply,
  strictKeys,
  type FakeServer,
} from "../test/llm/conformance-helpers";

// ─── Vector shapes ───────────────────────────────────────────────────────────

interface VMessage {
  role: string;
  content?: string;
  parts?: { type: string; text?: string; mime_type?: string; data?: string }[];
  tool_calls?: { id?: string; name: string; arguments?: string }[];
  tool_call_id?: string;
  cache_control?: { ttl: string };
  thinking?: { thinking?: string; signature?: string; redacted?: string }[];
}

interface VRequest {
  model?: string;
  temperature?: number;
  max_tokens?: number;
  stop_sequences?: string[];
  messages: VMessage[];
  thinking_budget?: number;
}

interface VTool {
  name: string;
  description?: string;
  parameters?: string;
  cache_control?: { ttl: string };
}

interface VResponse {
  status: number;
  headers: Record<string, string>;
  body: string;
}

type Section = Record<string, unknown>[];

interface File {
  version: string;
  description: string;
  [section: string]: unknown;
}

/** Sections run here, by sibling runners, and not at all (with why). */
const RUN = [
  "redact_uri", "usage_cost", "credential_headers",
  "anthropic_request", "anthropic_request_error", "anthropic_headers",
  "anthropic_stream", "anthropic_accumulate", "anthropic_error",
  "anthropic_limits", "anthropic_response", "anthropic_tool_names",
];
const SIBLING = ["tool_names"];
const NOT_RUN: Record<string, string> = {
  parse_uri:
    "core parseURI keeps its own error texts and rejects an empty model; adapters are unaffected",
  key_endpoint:
    "core has no llm.yaml, ApplyAPIKey or Resolve-time key guard; adapters guard keys instead",
  anthropic_tool_choice:
    "pending the TypeScript port of the Go 403 split / system joining / stream events / hermetic / metadata-only bus changes",
  anthropic_hermetic:
    "pending the TypeScript port of the Go 403 split / system joining / stream events / hermetic / metadata-only bus changes",
  anthropic_stream_events:
    "pending the TypeScript port of the Go 403 split / system joining / stream events / hermetic / metadata-only bus changes",
  bus_request_started:
    "pending the TypeScript port of the Go 403 split / system joining / stream events / hermetic / metadata-only bus changes",
  bus_response_received:
    "pending the TypeScript port of the Go 403 split / system joining / stream events / hermetic / metadata-only bus changes",
};
/** Single vectors not run, with why. */
const NOT_RUN_VECTORS: Record<string, string> = {
  "credential_headers/llm.yaml base_url": "core has no llm.yaml layer",
  "anthropic_error/403 permission denied": "403 permission-denied split pending the TypeScript port",
  "anthropic_error/stream: 403 before any event": "403 permission-denied split pending the TypeScript port",
  "anthropic_stream/permission error event mid-stream": "403 permission-denied split pending the TypeScript port",
  "anthropic_request/system messages join in order": "system-message joining pending the TypeScript port",
  "anthropic_request/empty system messages are skipped": "system-message joining pending the TypeScript port",
  "anthropic_request/only empty system messages send no system": "system-message joining pending the TypeScript port",
  "anthropic_request/system cache breakpoints split the joined text": "system-message joining pending the TypeScript port",
};

const file = loadVectors<File>();
const section = (name: string): Section => file[name] as Section;
const CONF_KEY = "conf-key-must-not-leak";

describe("llm-v1 conformance file", () => {
  it("is v1 and every section has a runner or a stated reason", () => {
    expect(file.version).toBe("v1");
    for (const k of Object.keys(file)) {
      if (k === "version" || k === "description") continue;
      expect(
        RUN.includes(k) || SIBLING.includes(k) || k in NOT_RUN,
        `section ${k} has no runner`
      ).toBe(true);
    }
    for (const s of RUN) expect(section(s)?.length, s).toBeGreaterThan(0);
  });
});

// ─── Conversions ─────────────────────────────────────────────────────────────

function toMessage(m: VMessage): Message {
  strictKeys(m, [
    "role", "content", "parts", "tool_calls", "tool_call_id", "cache_control", "thinking",
  ], "message");
  const out: Message = { role: m.role, content: m.content ?? "" };
  if (m.parts) {
    out.parts = m.parts.map((p) => {
      strictKeys(p, ["type", "text", "mime_type", "data"], "part");
      return {
        type: p.type as "text",
        text: p.text,
        mimeType: p.mime_type,
        source: p.data
          ? inlineSource(new Uint8Array(Buffer.from(p.data, "base64")), p.mime_type ?? "")
          : undefined,
      };
    });
  }
  if (m.tool_calls) {
    out.toolCalls = m.tool_calls.map(
      (tc): ToolCall => ({ id: tc.id ?? "", name: tc.name, arguments: tc.arguments })
    );
  }
  if (m.tool_call_id) out.toolCallId = m.tool_call_id;
  if (m.cache_control) out.cacheControl = { ttl: m.cache_control.ttl };
  if (m.thinking) {
    out.thinking = m.thinking.map((t) => ({
      thinking: t.thinking ?? "",
      signature: t.signature ?? "",
      redacted: t.redacted ?? "",
    }));
  }
  return out;
}

function toRequest(r: VRequest): Request {
  strictKeys(r, [
    "model", "temperature", "max_tokens", "stop_sequences", "messages", "thinking_budget",
  ], "request");
  const out: Request = { messages: r.messages.map(toMessage) };
  if (r.model) out.model = r.model;
  if (r.temperature !== undefined) out.temperature = r.temperature;
  if (r.max_tokens) out.maxTokens = r.max_tokens;
  if (r.stop_sequences) out.stopSequences = r.stop_sequences;
  if (r.thinking_budget) out.thinking = { budgetTokens: r.thinking_budget };
  return out;
}

function toTools(tools: VTool[]): ToolDef[] {
  return tools.map((t) => {
    strictKeys(t, ["name", "description", "parameters", "cache_control"], "tool");
    const d: ToolDef = { name: t.name, description: t.description ?? "", parameters: t.parameters };
    if (t.cache_control) d.cacheControl = { ttl: t.cache_control.ttl };
    return d;
  });
}

function adapter(
  base: string,
  opts: { model?: string; key?: string; params?: Record<string, string> } = {}
): AnthropicAdapter {
  const cfg: ResolvedConfig = {
    uri: { scheme: "anthropic", model: opts.model ?? "m" },
    provider: {
      apiKey: opts.key ?? "k",
      baseURL: base,
      model: opts.model ?? "m",
      params: opts.params,
    },
    fallbacks: [],
  };
  return new AnthropicAdapter(cfg);
}

const HI: Request = { messages: [{ role: "user", content: "Hi" }] };
const OK_MESSAGE = JSON.stringify({
  id: "msg_1", type: "message", role: "assistant", model: "m",
  content: [{ type: "text", text: "ok" }], stop_reason: "end_turn",
  usage: { input_tokens: 1, output_tokens: 1 },
});

/** Reads a stream to its end: tokens, thinking deltas, and the error. */
async function drain(s: AsyncIterable<{ content: string; thinking?: string; done: boolean }>) {
  const tokens: string[] = [];
  const thinking: string[] = [];
  let error: unknown;
  let done = false;
  try {
    for await (const t of s) {
      if (t.done) {
        done = true;
        break;
      }
      if (t.thinking) thinking.push(t.thinking);
      else tokens.push(t.content);
    }
  } catch (e) {
    error = e;
  }
  return { tokens, thinking, error, done };
}

function errorKind(err: unknown): [string, number] {
  if (err === undefined || err === null) return ["", 0];
  if (err instanceof StreamTruncatedError) return ["truncated", 0];
  if (err instanceof ResponseTooLargeError) return ["too_large", 0];
  if (err instanceof AuthError) return ["auth", 0];
  if (err instanceof RateLimitError) return ["rate_limit", 0];
  if (err instanceof ModelError) return ["model", 0];
  if (err instanceof HTTPStatusError) return ["http_status", err.statusCode ?? 0];
  if (err instanceof APIError) return ["api", err.statusCode ?? 0];
  return ["other", 0];
}

let restoreEnv: () => void;
const servers: FakeServer[] = [];
beforeEach(() => {
  restoreEnv = isolateEnv();
});
afterEach(async () => {
  restoreEnv();
  await Promise.all(servers.splice(0).map((s) => s.close()));
});

async function server(
  handler: Parameters<typeof fakeServer>[0]
): Promise<FakeServer> {
  const s = await fakeServer(handler);
  servers.push(s);
  return s;
}

const sseServer = (body: string) =>
  server((_h, res) => reply(res, "text/event-stream", body));

// ─── Provider-neutral sections ───────────────────────────────────────────────

describe("llm-v1 conformance: redact_uri", () => {
  for (const v of section("redact_uri") as { name: string; in: string; want: string }[]) {
    it(v.name, () => {
      strictKeys(v, ["name", "in", "want"], v.name);
      expect(redactURI(v.in)).toBe(v.want);
    });
  }
});

describe("llm-v1 conformance: usage_cost", () => {
  type V = {
    name: string;
    usage: Record<string, number>;
    price: Record<string, number>;
    want: number;
  };
  for (const v of section("usage_cost") as V[]) {
    it(v.name, () => {
      strictKeys(v, ["name", "usage", "price", "want"], v.name);
      const got = usageCost(
        {
          promptTokens: v.usage["prompt_tokens"]!,
          completionTokens: v.usage["completion_tokens"]!,
          cacheReadTokens: v.usage["cache_read_tokens"]!,
          cacheCreationTokens: v.usage["cache_creation_tokens"]!,
          totalTokens: 0,
        },
        {
          input: v.price["input"]!,
          output: v.price["output"]!,
          cacheRead: v.price["cache_read"]!,
          cacheWrite: v.price["cache_write"]!,
        }
      );
      expect(got).toBeCloseTo(v.want, 9);
    });
  }
});

// ─── anthropic_* ─────────────────────────────────────────────────────────────

describe("llm-v1 conformance: anthropic_request", () => {
  type V = {
    name: string;
    adapter_model: string;
    request: VRequest;
    tools?: VTool[];
    want_body: unknown;
  };
  for (const v of section("anthropic_request") as V[]) {
    const why = NOT_RUN_VECTORS[`anthropic_request/${v.name}`];
    if (why) {
      it.skip(`${v.name} (${why})`, () => {});
      continue;
    }
    it(v.name, async () => {
      strictKeys(v, ["name", "adapter_model", "request", "tools", "want_body"], v.name);
      const s = await server((_h, res) => reply(res, "application/json", OK_MESSAGE));
      const a = adapter(s.url, { model: v.adapter_model });
      const req = toRequest(v.request);
      if (v.tools) await a.callWithTools(req, toTools(v.tools));
      else await a.complete(req);
      expect(s.hits).toHaveLength(1);
      expect(JSON.parse(s.hits[0]!.body)).toEqual(v.want_body);
    });
  }
});

describe("llm-v1 conformance: anthropic_request_error", () => {
  type V = { name: string; request: VRequest; error: string };
  for (const v of section("anthropic_request_error") as V[]) {
    it(v.name, async () => {
      strictKeys(v, ["name", "request", "error"], v.name);
      const s = await server((_h, res) => reply(res, "application/json", OK_MESSAGE));
      const err = await adapter(s.url)
        .complete(toRequest(v.request))
        .then(() => undefined, (e: unknown) => e);
      expect(err).toBeInstanceOf(Error);
      expect((err as Error).message).toBe(v.error);
      expect(s.hits, "a rejected request is never sent").toHaveLength(0);
    });
  }
});

describe("llm-v1 conformance: anthropic_headers", () => {
  type V = {
    name: string;
    api_key: string;
    env: Record<string, string>;
    want: Record<string, string>;
    absent: string[];
  };
  for (const v of section("anthropic_headers") as V[]) {
    it(v.name, async () => {
      strictKeys(v, ["name", "api_key", "env", "want", "absent"], v.name);
      Object.assign(process.env, v.env);
      const s = await server((_h, res) => reply(res, "application/json", OK_MESSAGE));
      await adapter(s.url, { key: v.api_key }).complete(HI);
      const got = s.hits[0]!.headers;
      for (const [k, want] of Object.entries(v.want)) expect(got[k], k).toBe(want);
      for (const k of v.absent) expect(got[k], k).toBeUndefined();
    });
  }
});

describe("llm-v1 conformance: credential_headers", () => {
  type V = {
    name: string;
    base_url: string;
    yaml: string;
    env: Record<string, string>;
    sent: string[];
    withheld: string[];
    refused: boolean;
  };
  for (const v of section("credential_headers") as V[]) {
    const skip = NOT_RUN_VECTORS[`credential_headers/${v.name}`];
    if (skip) {
      it(v.name, () => {
        // Stated, not silent: the vector needs a layer this SDK lacks.
        expect(v.yaml, skip).not.toBe("");
      });
      continue;
    }
    it(v.name, async () => {
      strictKeys(v, ["name", "base_url", "yaml", "env", "sent", "withheld", "refused"], v.name);
      expect(v.yaml, "yaml vectors are listed in NOT_RUN_VECTORS").toBe("");
      Object.assign(process.env, v.env);
      // Intercept fetch: the vectors name hosts no test may reach.
      const sent: Headers[] = [];
      const orig = globalThis.fetch;
      globalThis.fetch = (async (_u: unknown, init?: RequestInit) => {
        sent.push(new Headers(init?.headers));
        return new globalThis.Response(OK_MESSAGE, {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }) as typeof fetch;
      try {
        let a: AnthropicAdapter;
        try {
          a = adapter(v.base_url, { key: "kit-key" });
        } catch (e) {
          expect(v.refused, String(e)).toBe(true);
          expect(e).toBeInstanceOf(KeyEndpointError);
          return;
        }
        expect(v.refused).toBe(false);
        await a.complete(HI);
      } finally {
        globalThis.fetch = orig;
      }
      expect(sent).toHaveLength(1);
      for (const h of v.sent) expect(sent[0]!.get(h), h).toBeTruthy();
      for (const h of v.withheld) expect(sent[0]!.get(h), h).toBeNull();
    });
  }
});

describe("llm-v1 conformance: anthropic_stream", () => {
  type V = {
    name: string;
    sse: string;
    want_tokens: string[];
    want_error: boolean;
    error_kind: string;
    thinking_budget: number;
    want_thinking: string[];
  };
  for (const v of section("anthropic_stream") as V[]) {
    const why = NOT_RUN_VECTORS[`anthropic_stream/${v.name}`];
    if (why) {
      it.skip(`${v.name} (${why})`, () => {});
      continue;
    }
    it(v.name, async () => {
      strictKeys(v, [
        "name", "sse", "want_tokens", "want_error", "error_kind", "thinking_budget", "want_thinking",
      ], v.name);
      const s = await sseServer(v.sse);
      const req: Request = { ...HI };
      if (v.thinking_budget > 0) {
        req.maxTokens = 4 * v.thinking_budget;
        req.thinking = { budgetTokens: v.thinking_budget };
      }
      const st = adapter(s.url).stream(req);
      const r = await drain(st);
      expect(r.tokens).toEqual(v.want_tokens);
      expect(r.thinking).toEqual(v.want_thinking);
      expect(r.error !== undefined, String(r.error)).toBe(v.want_error);
      expect(errorKind(r.error)[0], String(r.error)).toBe(v.error_kind);
      if (r.done) {
        // Nothing follows the final token.
        expect((await st[Symbol.asyncIterator]().next()).done).toBe(true);
      }
    });
  }
});

describe("llm-v1 conformance: anthropic_accumulate", () => {
  type W = {
    text?: string;
    thinking?: string;
    tool_calls?: { id: string; name: string; input: unknown }[];
    stop_reason?: string;
    usage?: Record<string, number>;
    error: boolean;
    thinking_blocks: { thinking: string; signature: string; redacted: string }[] | null;
  };
  type V = { name: string; sse?: string; sse_from?: string; want: W };
  const streams = new Map(
    (section("anthropic_stream") as { name: string; sse: string }[]).map((s) => [
      `anthropic_stream/${s.name}`,
      s.sse,
    ])
  );
  for (const v of section("anthropic_accumulate") as V[]) {
    it(v.name, async () => {
      strictKeys(v, ["name", "sse", "sse_from", "want"], v.name);
      strictKeys(v.want, [
        "text", "thinking", "tool_calls", "stop_reason", "usage", "error", "thinking_blocks",
      ], v.name);
      const sse = v.sse_from ? streams.get(v.sse_from) : v.sse;
      expect(sse, `sse_from ${v.sse_from}`).toBeDefined();
      const s = await sseServer(sse!);
      const st: ToolStream = adapter(s.url).streamWithTools(HI, [
        { name: "get_weather", description: "", parameters: undefined },
      ]);
      const r = await drain(st);
      expect(r.error !== undefined, String(r.error)).toBe(v.want.error);
      if (v.want.error) {
        expect(st.outcome(), "no outcome after an error").toBeUndefined();
        return;
      }
      const out = st.outcome();
      expect(out).toBeDefined();
      expect(out!.content).toBe(v.want.text);
      expect(out!.thinking.map((b) => b.thinking).join("")).toBe(v.want.thinking);
      expect(out!.finishReason).toBe(v.want.stop_reason);
      expect(out!.usage.promptTokens).toBe(v.want.usage!["input_tokens"]);
      expect(out!.usage.completionTokens).toBe(v.want.usage!["output_tokens"]);
      expect(out!.usage.cacheReadTokens).toBe(v.want.usage!["cache_read_tokens"]);
      expect(out!.usage.cacheCreationTokens).toBe(v.want.usage!["cache_creation_tokens"]);
      if (v.want.thinking_blocks !== null) {
        expect(out!.thinking).toEqual(v.want.thinking_blocks);
      }
      expect(out!.toolCalls.map((c) => ({ id: c.id, name: c.name, input: c.arguments }))).toEqual(
        v.want.tool_calls
      );
    });
  }
});

describe("llm-v1 conformance: anthropic_error", () => {
  type V = {
    name: string;
    responses: VResponse[];
    want_kind: string;
    want_status: number;
    fallbackable: boolean;
    attempts: number;
    stream: boolean;
  };
  for (const v of section("anthropic_error") as V[]) {
    const why = NOT_RUN_VECTORS[`anthropic_error/${v.name}`];
    if (why) {
      it.skip(`${v.name} (${why})`, () => {});
      continue;
    }
    it(v.name, { timeout: 10_000 }, async () => {
      strictKeys(v, [
        "name", "responses", "want_kind", "want_status", "fallbackable", "attempts", "stream",
      ], v.name);
      const s = await server((_h, res, n) => {
        const r = v.responses[Math.min(n, v.responses.length - 1)]!;
        const headers: Record<string, string> = { ...r.headers };
        if (!Object.keys(headers).some((k) => k.toLowerCase() === "content-type")) {
          headers["content-type"] = "application/json";
        }
        res.writeHead(r.status, headers);
        res.end(r.body);
      });
      const a = adapter(s.url, { key: CONF_KEY });
      let err: unknown;
      if (v.stream) {
        err = (await drain(a.stream(HI))).error;
      } else {
        err = await a.complete(HI).then(() => undefined, (e: unknown) => e);
      }
      if (err instanceof Error) {
        expect(err.message).not.toContain(CONF_KEY);
        expect(String(err.stack)).not.toContain(CONF_KEY);
        expect(String((err as { cause?: Error }).cause?.message)).not.toContain(CONF_KEY);
      }
      const [kind, status] = errorKind(err);
      expect(kind, String(err)).toBe(v.want_kind);
      expect(status).toBe(v.want_status);
      expect(err instanceof Error ? isFallbackable(err) : false).toBe(v.fallbackable);
      expect(s.hits).toHaveLength(v.attempts);
    });
  }
});

describe("llm-v1 conformance: anthropic_limits", () => {
  type V = {
    name: string;
    params: Record<string, string>;
    stream: boolean;
    content_type: string;
    body: string;
    too_large: boolean;
  };
  for (const v of section("anthropic_limits") as V[]) {
    it(v.name, async () => {
      strictKeys(v, ["name", "params", "stream", "content_type", "body", "too_large"], v.name);
      const s = await server((_h, res) => reply(res, v.content_type, v.body));
      const a = adapter(s.url, { params: v.params });
      let err: unknown;
      if (v.stream) err = (await drain(a.stream(HI))).error;
      else err = await a.complete(HI).then(() => undefined, (e: unknown) => e);
      if (v.too_large) {
        expect(errorKind(err)[0], String(err)).toBe("too_large");
        expect(s.hits, "an overrun is not retried").toHaveLength(1);
        return;
      }
      expect(err).toBeUndefined();
    });
  }
});

describe("llm-v1 conformance: anthropic_response", () => {
  type V = {
    name: string;
    tools: boolean;
    body: string;
    want: {
      content: string;
      thinking_blocks: { thinking: string; signature: string; redacted: string }[];
      tool_calls: { id: string; name: string; input: unknown }[];
      stop_reason: string;
      usage: Record<string, number>;
    };
  };
  for (const v of section("anthropic_response") as V[]) {
    it(v.name, async () => {
      strictKeys(v, ["name", "tools", "body", "want"], v.name);
      const s = await server((_h, res) => reply(res, "application/json", v.body));
      const a = adapter(s.url);
      const got = v.tools
        ? await a.callWithTools(HI, [{ name: "f", description: "", parameters: undefined }])
        : { ...(await a.complete(HI)), toolCalls: [] as ToolCall[] };
      expect(got.content).toBe(v.want.content);
      expect(got.finishReason).toBe(v.want.stop_reason);
      expect(got.thinking).toEqual(v.want.thinking_blocks);
      expect(got.usage).toEqual({
        promptTokens: v.want.usage["prompt_tokens"],
        completionTokens: v.want.usage["completion_tokens"],
        cacheReadTokens: v.want.usage["cache_read_tokens"],
        cacheCreationTokens: v.want.usage["cache_creation_tokens"],
        totalTokens: v.want.usage["total_tokens"],
      });
      expect(got.toolCalls.map((c) => ({ id: c.id, name: c.name, input: c.arguments }))).toEqual(
        v.want.tool_calls
      );
    });
  }
});

describe("llm-v1 conformance: anthropic_tool_names", () => {
  type V = {
    name: string;
    call: "tools" | "stream_tools" | "complete" | "stream";
    params?: Record<string, string>;
    messages?: VMessage[];
    tools?: string[];
    body?: string;
    want_tools?: string[];
    want_history?: string[];
    want_calls?: { id: string; name: string }[];
    error?: { reason: string; name: string; other: string };
    refused?: boolean;
  };

  function sentNames(body: string): { tools?: string[]; history?: string[] } {
    const b = JSON.parse(body) as {
      tools?: { name: string }[];
      messages: { content: { type: string; name?: string }[] }[];
    };
    const history = b.messages.flatMap((m) =>
      m.content.filter((c) => c.type === "tool_use").map((c) => c.name!)
    );
    return {
      tools: b.tools?.map((t) => t.name),
      history: history.length ? history : undefined,
    };
  }

  for (const v of section("anthropic_tool_names") as V[]) {
    it(v.name, async () => {
      strictKeys(v, [
        "name", "call", "params", "messages", "tools", "body", "want_tools",
        "want_history", "want_calls", "error", "refused",
      ], v.name);
      const streaming = v.call === "stream" || v.call === "stream_tools";
      const s = await server((_h, res) =>
        reply(res, streaming ? "text/event-stream" : "application/json", v.body ?? "")
      );
      let a: AnthropicAdapter;
      try {
        a = adapter(s.url, { params: v.params });
      } catch (e) {
        expect(v.refused, String(e)).toBe(true);
        return;
      }
      expect(v.refused ?? false).toBe(false);

      const req: Request = v.messages ? toRequest({ messages: v.messages }) : HI;
      const defs: ToolDef[] = (v.tools ?? []).map((name) => ({
        name,
        description: "",
        parameters: undefined,
      }));
      let calls: ToolCall[] = [];
      let err: unknown;
      try {
        switch (v.call) {
          case "tools":
            calls = (await a.callWithTools(req, defs)).toolCalls;
            break;
          case "stream_tools": {
            const st = a.streamWithTools(req, defs);
            const r = await drain(st);
            if (r.error) throw r.error;
            calls = st.outcome()!.toolCalls;
            break;
          }
          case "complete":
            await a.complete(req);
            break;
          case "stream": {
            const r = await drain(a.stream(req));
            if (r.error) throw r.error;
            break;
          }
        }
      } catch (e) {
        err = e;
      }

      if (v.error) {
        expect(err).toBeInstanceOf(ToolNameError);
        const te = err as ToolNameError;
        expect(te.reason).toBe(v.error.reason);
        expect(te.toolName).toBe(v.error.name);
        expect(te.other).toBe(v.error.other);
        expect(isFallbackable(te)).toBe(false);
        expect(s.hits, "a rejected request reaches no server").toHaveLength(0);
        return;
      }
      expect(err).toBeUndefined();
      const sent = sentNames(s.hits[0]!.body);
      expect(sent.tools).toEqual(v.want_tools);
      expect(sent.history).toEqual(v.want_history);
      expect(calls.map((c) => ({ id: c.id, name: c.name }))).toEqual(v.want_calls ?? []);
    });
  }
});
