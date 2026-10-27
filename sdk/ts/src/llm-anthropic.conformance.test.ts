// Runs contracts/llm-v1/vectors.json against this SDK: every
// anthropic_* section through AnthropicAdapter and loopback fake
// servers, credential_headers, key_endpoint, parse_uri, redact_uri,
// usage_cost, and the bus_* payloads through the pure builders (this
// client publishes no bus events). Go is canonical; a failure means
// this SDK drifted, not that a vector should change. tool_names runs
// in llm-toolnames.conformance.test.ts, media_fetch in
// llm-media.conformance.test.ts. A section in neither list fails the
// run, so none is skipped silently.

import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

import {
  APIError,
  AuthError,
  LLMError,
  HTTPStatusError,
  KeyEndpointError,
  ModelError,
  PermissionDeniedError,
  RateLimitError,
  ResponseTooLargeError,
  StreamTruncatedError,
  ToolNameError,
  inlineSource,
  applyAPIKey,
  isFallbackable,
  loadConfig,
  parseURI,
  redactURI,
  resolve,
  requestStartedPayload,
  responseReceivedPayload,
  usageCost,
  type CallOp,
  type MediaSource,
  type Message,
  type Request,
  type ResolvedConfig,
  type Token,
  type ToolCall,
  type ToolChoiceMode,
  type ToolDef,
  type ToolStream,
  type Usage,
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
  parts?: { type: string; text?: string; mime_type?: string; data?: string; url?: string }[];
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
  extensions?: Record<string, unknown>;
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

/** Sections run here and by sibling runners. */
const RUN = [
  "redact_uri", "parse_uri", "usage_cost", "credential_headers", "key_endpoint",
  "anthropic_request", "anthropic_request_error", "anthropic_headers",
  "anthropic_stream", "anthropic_accumulate", "anthropic_error",
  "anthropic_limits", "anthropic_response", "anthropic_tool_names",
  "anthropic_tool_choice", "anthropic_hermetic", "anthropic_stream_events",
  "bus_request_started", "bus_response_received",
];
const SIBLING = ["tool_names", "media_fetch", "gemini_stream_events"];

const file = loadVectors<File>();
const section = (name: string): Section => file[name] as Section;
const CONF_KEY = "conf-key-must-not-leak";

describe("llm-v1 conformance file", () => {
  it("is v1 and every section has a runner or a stated reason", () => {
    expect(file.version).toBe("v1");
    for (const k of Object.keys(file)) {
      if (k === "version" || k === "description") continue;
      expect(
        RUN.includes(k) || SIBLING.includes(k),
        `section ${k} has no runner`
      ).toBe(true);
    }
    for (const s of RUN) expect(section(s)?.length, s).toBeGreaterThan(0);
  });
});

// ─── Conversions ─────────────────────────────────────────────────────────────

/** A URL media source the adapter must send as a URL: reading it fails. */
function unfetchedURL(url: string): MediaSource {
  return {
    url: () => url,
    mimeType: () => "",
    read: async () => {
      throw new Error(`url source ${url} was fetched; it must be sent as a url source`);
    },
  };
}

function toMessage(m: VMessage): Message {
  strictKeys(m, [
    "role", "content", "parts", "tool_calls", "tool_call_id", "cache_control", "thinking",
  ], "message");
  const out: Message = { role: m.role, content: m.content ?? "" };
  if (m.parts) {
    out.parts = m.parts.map((p) => {
      strictKeys(p, ["type", "text", "mime_type", "data", "url"], "part");
      return {
        type: p.type as "text",
        text: p.text,
        mimeType: p.mime_type,
        source: p.url
          ? unfetchedURL(p.url)
          : p.data
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
    "extensions",
  ], "request");
  const out: Request = { messages: r.messages.map(toMessage) };
  if (r.model) out.model = r.model;
  if (r.temperature !== undefined) out.temperature = r.temperature;
  if (r.max_tokens) out.maxTokens = r.max_tokens;
  if (r.stop_sequences) out.stopSequences = r.stop_sequences;
  if (r.thinking_budget) out.thinking = { budgetTokens: r.thinking_budget };
  if (r.extensions) out.extensions = r.extensions;
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
  if (err instanceof PermissionDeniedError) {
    // A permission denial is an auth error too.
    return [err instanceof AuthError ? "permission_denied" : "permission_denied without auth", 0];
  }
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

/**
 * Points XDG_CONFIG_HOME at a fresh dir holding `yaml` as hop/llm.yaml
 * (none when empty), where Go reads it. Restored after each test.
 */
function useLLMYaml(yaml: string): void {
  const xdg = mkdtempSync(join(tmpdir(), "kit-llm-conf-"));
  const restore = isolateEnv(["XDG_CONFIG_HOME"]);
  process.env["XDG_CONFIG_HOME"] = xdg;
  if (yaml !== "") {
    mkdirSync(join(xdg, "hop"), { recursive: true });
    writeFileSync(join(xdg, "hop", "llm.yaml"), yaml);
  }
  cleanups.push(() => {
    restore();
    rmSync(xdg, { recursive: true, force: true });
  });
}
const cleanups: (() => void)[] = [];
afterEach(() => {
  for (const c of cleanups.splice(0).reverse()) c();
});

// ─── Provider-neutral sections ───────────────────────────────────────────────

describe("llm-v1 conformance: redact_uri", () => {
  for (const v of section("redact_uri") as { name: string; in: string; want: string }[]) {
    it(v.name, () => {
      strictKeys(v, ["name", "in", "want"], v.name);
      expect(redactURI(v.in)).toBe(v.want);
    });
  }
});

describe("llm-v1 conformance: parse_uri", () => {
  type V = {
    name: string;
    uri: string;
    scheme?: string;
    model?: string;
    host?: string;
    params?: Record<string, string> | null;
    error: string;
  };
  for (const v of section("parse_uri") as V[]) {
    it(v.name, () => {
      strictKeys(v, ["name", "uri", "scheme", "model", "host", "params", "error"], v.name);
      if (v.error) {
        let err: unknown;
        try {
          parseURI(v.uri);
        } catch (e) {
          err = e;
        }
        expect(err).toBeInstanceOf(LLMError);
        expect((err as Error).message).toBe(v.error);
        return;
      }
      const u = parseURI(v.uri);
      expect(u.scheme).toBe(v.scheme);
      expect(u.model).toBe(v.model);
      expect(u.host ?? "").toBe(v.host);
      expect(u.params ?? {}).toEqual(v.params ?? {});
    });
  }
});

describe("llm-v1 conformance: key_endpoint", () => {
  type V = {
    name: string;
    op: string;
    uri: string;
    yaml: string;
    env: Record<string, string>;
    want_uri: string;
    refused: boolean;
  };
  for (const v of section("key_endpoint") as V[]) {
    it(v.name, () => {
      strictKeys(v, ["name", "op", "uri", "yaml", "env", "want_uri", "refused"], v.name);
      useLLMYaml(v.yaml);
      Object.assign(process.env, v.env);
      let got: string | undefined;
      let err: unknown;
      try {
        switch (v.op) {
          case "apply":
            got = applyAPIKey(v.uri);
            break;
          case "load":
            loadConfig(v.uri);
            break;
          case "resolve":
            resolve(v.uri);
            break;
          default:
            throw new Error(`unknown op ${JSON.stringify(v.op)}`);
        }
      } catch (e) {
        err = e;
      }
      if (v.refused) {
        expect(err).toBeInstanceOf(KeyEndpointError);
        expect((err as Error).message).not.toContain("fake-env-key");
        return;
      }
      expect(err).toBeUndefined();
      if (v.op === "apply") expect(got).toBe(v.want_uri);
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
    it(v.name, async () => {
      strictKeys(v, ["name", "base_url", "yaml", "env", "sent", "withheld", "refused"], v.name);
      useLLMYaml(v.yaml);
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

// A complete, empty streamed turn.
const MINIMAL_SSE =
  'event: message_start\ndata: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"m","stop_reason":null,"usage":{"input_tokens":1,"output_tokens":0}}}\n\n' +
  'event: message_stop\ndata: {"type":"message_stop"}\n\n';

const toolDefs = (names: string[]): ToolDef[] =>
  names.map((name) => ({ name, description: "", parameters: undefined }));

describe("llm-v1 conformance: anthropic_tool_choice", () => {
  type V = {
    name: string;
    call: "complete" | "stream" | "tools" | "stream_tools";
    tools: string[];
    tool_choice: { mode: string; name?: string } | null;
    want_tool_choice?: unknown;
    error?: string;
  };
  for (const v of section("anthropic_tool_choice") as V[]) {
    it(v.name, async () => {
      strictKeys(v, ["name", "call", "tools", "tool_choice", "want_tool_choice", "error"], v.name);
      const streaming = v.call === "stream" || v.call === "stream_tools";
      const s = await server((_h, res) =>
        streaming ? reply(res, "text/event-stream", MINIMAL_SSE) : reply(res, "application/json", OK_MESSAGE)
      );
      const a = adapter(s.url);
      const req: Request = { ...HI };
      if (v.tool_choice) {
        strictKeys(v.tool_choice, ["mode", "name"], v.name);
        req.toolChoice = { mode: v.tool_choice.mode as ToolChoiceMode };
        if (v.tool_choice.name !== undefined) req.toolChoice.name = v.tool_choice.name;
      }
      const tools = toolDefs(v.tools);
      let err: unknown;
      try {
        switch (v.call) {
          case "complete":
            await a.complete(req);
            break;
          case "tools":
            await a.callWithTools(req, tools);
            break;
          case "stream": {
            const r = await drain(a.stream(req));
            if (r.error) throw r.error;
            break;
          }
          case "stream_tools": {
            const r = await drain(a.streamWithTools(req, tools));
            if (r.error) throw r.error;
            break;
          }
          default:
            throw new Error(`unknown call ${JSON.stringify(v.call)}`);
        }
      } catch (e) {
        err = e;
      }
      if (v.error) {
        expect(err).toBeInstanceOf(Error);
        expect((err as Error).message).toBe(v.error);
        expect(s.hits, "a rejected request is never sent").toHaveLength(0);
        return;
      }
      expect(err).toBeUndefined();
      const body = JSON.parse(s.hits[0]!.body) as { tool_choice?: unknown };
      if (v.want_tool_choice === null || v.want_tool_choice === undefined) {
        expect("tool_choice" in body, "no tool_choice sent").toBe(false);
        return;
      }
      expect(body.tool_choice).toEqual(v.want_tool_choice);
    });
  }
});

describe("llm-v1 conformance: anthropic_hermetic", () => {
  type V = {
    name: string;
    base_url: string;
    yaml: string;
    env: Record<string, string>;
    want_host: string;
    sent: string[];
    withheld: string[];
  };
  for (const v of section("anthropic_hermetic") as V[]) {
    it(v.name, async () => {
      strictKeys(v, ["name", "base_url", "yaml", "env", "want_host", "sent", "withheld"], v.name);
      // Written where llm.yaml is read; a hermetic adapter must not.
      const xdg = mkdtempSync(join(tmpdir(), "kit-llm-hermetic-"));
      const restore = isolateEnv(["XDG_CONFIG_HOME"]);
      process.env["XDG_CONFIG_HOME"] = xdg;
      if (v.yaml !== "") {
        mkdirSync(join(xdg, "hop"), { recursive: true });
        writeFileSync(join(xdg, "hop", "llm.yaml"), v.yaml);
      }
      Object.assign(process.env, v.env);
      // Intercept fetch: the vectors name hosts no test may reach.
      const sent: { url: string; headers: Headers }[] = [];
      const orig = globalThis.fetch;
      globalThis.fetch = (async (u: unknown, init?: RequestInit) => {
        sent.push({ url: String(u), headers: new Headers(init?.headers) });
        return new globalThis.Response(OK_MESSAGE, {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }) as typeof fetch;
      try {
        const a = new AnthropicAdapter({
          uri: { scheme: "anthropic", model: "m" },
          provider: { apiKey: "kit-key", baseURL: v.base_url || undefined, model: "m" },
          fallbacks: [],
          hermetic: true,
        });
        await a.complete(HI);
      } finally {
        globalThis.fetch = orig;
        restore();
        rmSync(xdg, { recursive: true, force: true });
      }
      expect(sent).toHaveLength(1);
      expect(new URL(sent[0]!.url).host).toBe(v.want_host);
      for (const h of v.sent) expect(sent[0]!.headers.get(h), h).toBeTruthy();
      for (const h of v.withheld) expect(sent[0]!.headers.get(h), h).toBeNull();
    });
  }
});

function usageJSON(u: Usage): Record<string, number> {
  return {
    prompt_tokens: u.promptTokens,
    completion_tokens: u.completionTokens,
    cache_read_tokens: u.cacheReadTokens ?? 0,
    cache_creation_tokens: u.cacheCreationTokens ?? 0,
    total_tokens: u.totalTokens,
  };
}

/** One streamed token as the vectors spell it. */
function eventJSON(t: Token): Record<string, unknown> {
  if (t.done) return { type: "done" };
  const ev = t.toolCall;
  if (ev) {
    const out: Record<string, unknown> = { type: "tool_call", kind: ev.kind, index: ev.index };
    switch (ev.kind) {
      case "start":
        out["id"] = ev.id;
        out["name"] = ev.name;
        break;
      case "delta":
        out["arguments"] = ev.argumentsDelta;
        break;
      case "end":
        expect(ev.call, "end carries the call").toBeDefined();
        expect(ev.id, "end repeats the id").toBe(ev.call!.id);
        expect(ev.name, "end repeats the name").toBe(ev.call!.name);
        out["id"] = ev.call!.id;
        out["name"] = ev.call!.name;
        out["arguments"] = ev.call!.arguments;
        break;
    }
    return out;
  }
  if (t.usage) return { type: "usage", usage: usageJSON(t.usage) };
  if (t.thinking) return { type: "thinking", thinking: t.thinking };
  return { type: "text", text: t.content };
}

describe("llm-v1 conformance: anthropic_stream_events", () => {
  type V = {
    name: string;
    sse?: string;
    sse_from?: string;
    tools: string[];
    stream_events: boolean;
    want_events: unknown[];
    error_kind: string;
  };
  const streams = new Map(
    (section("anthropic_stream") as { name: string; sse: string }[]).map((s) => [
      `anthropic_stream/${s.name}`,
      s.sse,
    ])
  );
  for (const v of section("anthropic_stream_events") as V[]) {
    it(v.name, async () => {
      strictKeys(v, [
        "name", "sse", "sse_from", "tools", "stream_events", "want_events", "error_kind",
      ], v.name);
      const sse = v.sse_from ? streams.get(v.sse_from) : v.sse;
      expect(sse, `sse_from ${v.sse_from}`).toBeDefined();
      const s = await sseServer(sse!);
      const a = adapter(s.url);
      const req: Request = { ...HI, streamEvents: v.stream_events };
      const withTools = v.tools.length > 0;
      const st: ToolStream = withTools ? a.streamWithTools(req, toolDefs(v.tools)) : a.stream(req);
      const iter = st[Symbol.asyncIterator]();
      const got: unknown[] = [];
      const ends: { index: number; call: ToolCall }[] = [];
      let last: Usage | undefined;
      let err: unknown;
      try {
        for (;;) {
          const r = await iter.next();
          if (r.done) break;
          const t = r.value;
          got.push(eventJSON(t));
          if (t.toolCall?.kind === "end") ends.push({ index: t.toolCall.index, call: t.toolCall.call! });
          if (t.usage) last = t.usage;
          if (t.done) {
            expect((await iter.next()).done, "nothing follows done").toBe(true);
            break;
          }
        }
      } catch (e) {
        err = e;
      }
      expect(got).toEqual(v.want_events);
      expect(errorKind(err)[0], String(err)).toBe(v.error_kind);
      if (!withTools || err !== undefined) return;
      // The events and the outcome tell the same story.
      const out = st.outcome();
      expect(out).toBeDefined();
      if (v.stream_events) {
        expect(out!.toolCalls).toHaveLength(ends.length);
        for (const e of ends) expect(e.call, "end call is outcome.toolCalls[index]").toEqual(out!.toolCalls[e.index]);
        if (last) expect(out!.usage, "the last usage snapshot is the outcome's").toEqual(last);
      }
    });
  }
});

describe("llm-v1 conformance: bus_request_started", () => {
  type V = {
    name: string;
    op: CallOp;
    tools: string[];
    request: VRequest;
    want_payload: unknown;
  };
  for (const v of section("bus_request_started") as V[]) {
    it(v.name, () => {
      strictKeys(v, ["name", "op", "tools", "request", "want_payload"], v.name);
      const got = requestStartedPayload(v.op, toRequest(v.request), v.tools.length);
      expect(JSON.parse(JSON.stringify(got))).toEqual(v.want_payload);
    });
  }
});

describe("llm-v1 conformance: bus_response_received", () => {
  type V = {
    name: string;
    op: CallOp;
    response: {
      content: string;
      finish_reason: string;
      usage: Record<string, number>;
      thinking?: { thinking: string; signature: string }[];
    };
    tool_calls: { id: string; name: string; arguments: string }[];
    want_payload: Record<string, unknown>;
  };
  for (const v of section("bus_response_received") as V[]) {
    it(v.name, () => {
      strictKeys(v, ["name", "op", "response", "tool_calls", "want_payload"], v.name);
      const r = v.response;
      strictKeys(r, ["content", "finish_reason", "usage", "thinking"], v.name);
      const got = responseReceivedPayload(
        v.op,
        {
          content: r.content,
          role: "assistant",
          finishReason: r.finish_reason,
          usage: {
            promptTokens: r.usage["prompt_tokens"]!,
            completionTokens: r.usage["completion_tokens"]!,
            cacheReadTokens: r.usage["cache_read_tokens"]!,
            cacheCreationTokens: r.usage["cache_creation_tokens"]!,
            totalTokens: r.usage["total_tokens"]!,
          },
          thinking: (r.thinking ?? []).map((t) => ({ ...t, redacted: "" })),
        },
        v.tool_calls.map((c) => ({ id: c.id, name: c.name, arguments: c.arguments })),
        12.7
      );
      const payload = JSON.parse(JSON.stringify(got)) as Record<string, unknown>;
      // duration_ms is a non-negative integer, not compared.
      expect(Number.isInteger(payload["duration_ms"])).toBe(true);
      expect(payload["duration_ms"] as number).toBeGreaterThanOrEqual(0);
      delete payload["duration_ms"];
      expect(payload).toEqual(v.want_payload);
    });
  }
});
