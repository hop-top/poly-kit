// Runs the openai_* sections of contracts/llm-v1/vectors.json against
// OpenAIAdapter and loopback fake servers. Go is canonical; a failure
// means this SDK drifted, not that a vector should change.
// llm-anthropic.conformance.test.ts runs the other sections and counts
// these as run by a sibling.
//
// Vectors this SDK does not pass yet are listed in PENDING with the
// reason; they are skipped by name, never silently.

import { afterEach, beforeEach, describe, expect, it } from "vitest";

import {
  APIError,
  AuthError,
  HTTPStatusError,
  ModelError,
  PermissionDeniedError,
  RateLimitError,
  ResponseTooLargeError,
  StreamTruncatedError,
  isFallbackable,
  type Message,
  type Request,
  type ResolvedConfig,
  type Token,
  type ToolChoiceMode,
  type ToolDef,
  type ToolStream,
  type Usage,
} from "./llm";
import { OpenAIAdapter } from "./llm-openai";
import {
  fakeServer,
  isolateEnv,
  loadVectors,
  reply,
  strictKeys,
  type FakeServer,
} from "../test/llm/conformance-helpers";

/** The sections this runner owns. */
export const OPENAI_SECTIONS = [
  "openai_request", "openai_stream", "openai_stream_events",
  "openai_accumulate", "openai_error", "openai_limits",
];

const EVENTS =
  "the openai adapter streams no tool-call or usage events and sends no stream_options";

/** Vectors not run here, by section/name, with why. */
const PENDING: Record<string, string> = {
  "openai_request/stream with events asks for usage": EVENTS,
  "openai_request/stream_tools with events and tool_choice auto": EVENTS,
  "openai_stream_events/plain stream: usage from the final chunk": EVENTS,
  "openai_stream_events/tool call start, deltas, end on finish": EVENTS,
  "openai_stream_events/parallel calls interleave and end in index order": EVENTS,
  "openai_stream_events/call with no argument fragments ends with {}": EVENTS,
  "openai_stream_events/truncated: no end for the open call": EVENTS,
  "openai_stream_events/error chunk: no end for the open call": EVENTS,
};

type Section = { name: string }[];
const file = loadVectors<Record<string, unknown>>();
const section = <T>(name: string): (T & { name: string })[] => file[name] as (T & { name: string })[];
const CONF_KEY = "conf-key-must-not-leak";

/** `it`, or `it.skip` for a vector listed in PENDING. */
const test = (sec: string, name: string) => (PENDING[`${sec}/${name}`] ? it.skip : it);

describe("llm-v1 conformance: openai sections", () => {
  it("every openai_* section is run here; pending vectors exist", () => {
    const owned = Object.keys(file).filter((k) => k.startsWith("openai_"));
    expect(owned.sort()).toEqual([...OPENAI_SECTIONS].sort());
    const names = new Set(
      OPENAI_SECTIONS.flatMap((s) => (file[s] as Section).map((v) => `${s}/${v.name}`))
    );
    for (const k of Object.keys(PENDING)) expect(names.has(k), k).toBe(true);
  });
});

// ─── Helpers ─────────────────────────────────────────────────────────────────

interface VMessage {
  role: string;
  content?: string;
  tool_calls?: { id: string; name: string; arguments?: string }[];
  tool_call_id?: string;
}

interface VRequest {
  model?: string;
  temperature?: number;
  max_tokens?: number;
  stop_sequences?: string[];
  messages: VMessage[];
  tool_choice?: { mode: string; name?: string };
  stream_events?: boolean;
}

interface VTool {
  name: string;
  description?: string;
  parameters?: string;
}

interface VResponse {
  status: number;
  headers: Record<string, string>;
  body: string;
}

function toRequest(r: VRequest): Request {
  strictKeys(r, [
    "model", "temperature", "max_tokens", "stop_sequences", "messages", "tool_choice", "stream_events",
  ], "request");
  const messages: Message[] = r.messages.map((m) => {
    strictKeys(m, ["role", "content", "tool_calls", "tool_call_id"], "message");
    const msg: Message = { role: m.role as Message["role"], content: m.content ?? "" };
    if (m.tool_calls) {
      msg.toolCalls = m.tool_calls.map((c) => ({
        id: c.id,
        name: c.name,
        arguments: c.arguments ? JSON.parse(c.arguments) : {},
      }));
    }
    if (m.tool_call_id) msg.toolCallId = m.tool_call_id;
    return msg;
  });
  const req: Request = { messages };
  if (r.model) req.model = r.model;
  if (r.temperature !== undefined) req.temperature = r.temperature;
  if (r.max_tokens) req.maxTokens = r.max_tokens;
  if (r.stop_sequences) req.stopSequences = r.stop_sequences;
  if (r.tool_choice) {
    req.toolChoice = { mode: r.tool_choice.mode as ToolChoiceMode, name: r.tool_choice.name };
  }
  if (r.stream_events) req.streamEvents = true;
  return req;
}

function toTools(tools: VTool[]): ToolDef[] {
  return tools.map((t) => {
    strictKeys(t, ["name", "description", "parameters"], "tool");
    return { name: t.name, description: t.description ?? "", parameters: t.parameters || undefined };
  });
}

const named = (names: string[]): ToolDef[] =>
  names.map((name) => ({ name, description: "", parameters: undefined }));

function adapter(base: string, opts: { model?: string; params?: Record<string, string> } = {}) {
  const cfg: ResolvedConfig = {
    uri: { scheme: "openai", model: opts.model ?? "m" },
    provider: { apiKey: CONF_KEY, baseURL: base, model: opts.model ?? "m", params: opts.params },
    fallbacks: [],
  };
  return new OpenAIAdapter(cfg);
}

const HI: Request = { messages: [{ role: "user", content: "Hi" }] };

/** Reads a stream to its end: text tokens and the error. */
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

function errorKind(err: unknown): [string, number] {
  if (err === undefined || err === null) return ["", 0];
  if (err instanceof StreamTruncatedError) return ["truncated", 0];
  if (err instanceof ResponseTooLargeError) return ["too_large", 0];
  if (err instanceof PermissionDeniedError) {
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

async function server(handler: Parameters<typeof fakeServer>[0]): Promise<FakeServer> {
  const s = await fakeServer(handler);
  servers.push(s);
  return s;
}

const streams = new Map(
  section<{ sse: string }>("openai_stream").map((s) => [`openai_stream/${s.name}`, s.sse])
);
function sseOf(v: { sse?: string; sse_from?: string }): string {
  const sse = v.sse_from ? streams.get(v.sse_from) : v.sse;
  expect(sse, `sse_from ${v.sse_from}`).toBeDefined();
  return sse!;
}

const OK_COMPLETION = JSON.stringify({
  id: "c", object: "chat.completion",
  choices: [{ index: 0, message: { role: "assistant", content: "ok" }, finish_reason: "stop" }],
});
const OK_SSE = 'data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n';

// ─── Sections ────────────────────────────────────────────────────────────────

describe("llm-v1 conformance: openai_request", () => {
  type V = {
    call: string; adapter_model: string; request: VRequest; tools?: VTool[]; want_body: unknown;
  };
  for (const v of section<V>("openai_request")) {
    test("openai_request", v.name)(v.name, async () => {
      strictKeys(v, ["name", "call", "adapter_model", "request", "tools", "want_body"], v.name);
      const streamed = v.call === "stream" || v.call === "stream_tools";
      const s = await server((_h, res) =>
        streamed ? reply(res, "text/event-stream", OK_SSE) : reply(res, "application/json", OK_COMPLETION)
      );
      const a = adapter(s.url, { model: v.adapter_model });
      const req = toRequest(v.request);
      switch (v.call) {
        case "complete":
          await a.complete(req);
          break;
        case "tools":
          await a.callWithTools(req, toTools(v.tools ?? []));
          break;
        case "stream":
          expect((await drain(a.stream(req))).error).toBeUndefined();
          break;
        case "stream_tools":
          expect((await drain(a.streamWithTools(req, toTools(v.tools ?? [])))).error).toBeUndefined();
          break;
        default:
          throw new Error(`unknown call ${v.call}`);
      }
      expect(s.hits).toHaveLength(1);
      expect(s.hits[0]!.url).toBe("/chat/completions");
      expect(JSON.parse(s.hits[0]!.body)).toEqual(v.want_body);
    });
  }
});

describe("llm-v1 conformance: openai_stream", () => {
  type V = { sse: string; want_tokens: string[]; error_kind: string };
  for (const v of section<V>("openai_stream")) {
    test("openai_stream", v.name)(v.name, async () => {
      strictKeys(v, ["name", "sse", "want_tokens", "error_kind"], v.name);
      const s = await server((_h, res) => reply(res, "text/event-stream", v.sse));
      const { tokens, error } = await drain(adapter(s.url).stream(HI));
      expect(tokens).toEqual(v.want_tokens);
      expect(errorKind(error)[0], String(error)).toBe(v.error_kind);
      if (error instanceof Error) expect(error.message).not.toContain(CONF_KEY);
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
    if (ev.kind === "start") Object.assign(out, { id: ev.id, name: ev.name });
    else if (ev.kind === "delta") out["arguments"] = ev.argumentsDelta;
    else Object.assign(out, { id: ev.call!.id, name: ev.call!.name, arguments: ev.call!.arguments });
    return out;
  }
  if (t.usage) return { type: "usage", usage: usageJSON(t.usage) };
  return { type: "text", text: t.content };
}

describe("llm-v1 conformance: openai_stream_events", () => {
  type V = {
    sse?: string; sse_from?: string; tools: string[]; stream_events: boolean;
    want_events: unknown[]; error_kind: string;
  };
  for (const v of section<V>("openai_stream_events")) {
    test("openai_stream_events", v.name)(v.name, async () => {
      strictKeys(v, [
        "name", "sse", "sse_from", "tools", "stream_events", "want_events", "error_kind",
      ], v.name);
      const s = await server((_h, res) => reply(res, "text/event-stream", sseOf(v)));
      const a = adapter(s.url);
      const req: Request = { ...HI, streamEvents: v.stream_events };
      const st: ToolStream = v.tools.length ? a.streamWithTools(req, named(v.tools)) : a.stream(req);
      const got: unknown[] = [];
      let err: unknown;
      try {
        for await (const t of st) {
          got.push(eventJSON(t));
          if (t.done) break;
        }
      } catch (e) {
        err = e;
      }
      expect(got).toEqual(v.want_events);
      expect(errorKind(err)[0], String(err)).toBe(v.error_kind);
    });
  }
});

describe("llm-v1 conformance: openai_accumulate", () => {
  type V = {
    sse?: string; sse_from?: string; tools: string[];
    want: {
      text: string;
      tool_calls: { id: string; name: string; arguments: unknown }[];
      finish_reason: string;
      usage: { prompt_tokens: number; completion_tokens: number; total_tokens: number };
      error: boolean;
    };
  };
  for (const v of section<V>("openai_accumulate")) {
    test("openai_accumulate", v.name)(v.name, async () => {
      strictKeys(v, ["name", "sse", "sse_from", "tools", "want"], v.name);
      const s = await server((_h, res) => reply(res, "text/event-stream", sseOf(v)));
      const st = adapter(s.url).streamWithTools(HI, named(v.tools));
      const { error } = await drain(st);
      expect(error !== undefined, String(error)).toBe(v.want.error);
      if (v.want.error) {
        expect(st.outcome()).toBeUndefined();
        return;
      }
      const out = st.outcome()!;
      expect(out.content).toBe(v.want.text);
      expect(out.finishReason).toBe(v.want.finish_reason);
      expect([out.usage.promptTokens, out.usage.completionTokens, out.usage.totalTokens]).toEqual([
        v.want.usage.prompt_tokens, v.want.usage.completion_tokens, v.want.usage.total_tokens,
      ]);
      expect(out.toolCalls.map((c) => ({ id: c.id, name: c.name, arguments: c.arguments }))).toEqual(
        v.want.tool_calls
      );
    });
  }
});

describe("llm-v1 conformance: openai_error", () => {
  type V = {
    responses: VResponse[]; want_kind: string; want_status: number;
    fallbackable: boolean; attempts: number; stream: boolean;
  };
  for (const v of section<V>("openai_error")) {
    test("openai_error", v.name)(v.name, { timeout: 10_000 }, async () => {
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
      const a = adapter(s.url);
      const err = v.stream
        ? (await drain(a.stream(HI))).error
        : await a.complete(HI).then(() => undefined, (e: unknown) => e);
      if (err instanceof Error) expect(err.message).not.toContain(CONF_KEY);
      const [kind, status] = errorKind(err);
      expect(kind, String(err)).toBe(v.want_kind);
      expect(status).toBe(v.want_status);
      expect(err instanceof Error ? isFallbackable(err) : false).toBe(v.fallbackable);
      expect(s.hits).toHaveLength(v.attempts);
    });
  }
});

describe("llm-v1 conformance: openai_limits", () => {
  type V = {
    params: Record<string, string>; stream: boolean; content_type: string; body: string; too_large: boolean;
  };
  for (const v of section<V>("openai_limits")) {
    test("openai_limits", v.name)(v.name, async () => {
      strictKeys(v, ["name", "params", "stream", "content_type", "body", "too_large"], v.name);
      const s = await server((_h, res) => reply(res, v.content_type, v.body));
      const a = adapter(s.url, { params: v.params });
      const err = v.stream
        ? (await drain(a.stream(HI))).error
        : await a.complete(HI).then(() => undefined, (e: unknown) => e);
      expect(s.hits, "an overrun is not retried").toHaveLength(1);
      if (v.too_large) {
        expect(err, String(err)).toBeInstanceOf(ResponseTooLargeError);
        expect(isFallbackable(err as Error)).toBe(false);
        return;
      }
      expect(err).toBeUndefined();
    });
  }
});
