/**
 * @packageDocumentation
 *
 * RouteLLM adapter — registers the `routellm` URI scheme.
 *
 * URI format: `routellm://router_name:threshold` (e.g. `routellm://mf:0.7`).
 *
 * Delegates HTTP completions to an OpenAI-compatible endpoint on the
 * RouteLLM server, translating the model field into the server's
 * expected `router-[name]-[threshold]` format.
 */

import {
  type Completer,
  type Streamer,
  type Request,
  type Response,
  type Token,
  type ResolvedConfig,
  type Provider,
  type Message,
  type ContentPart,
  LLMError,
  checkToolLinkage,
  register,
} from "./llm";

// ─── RouterConfig ─────────────────────────────────────────────────────────────

export interface EvaConfig {
  contracts: string[];
  enforce: boolean;
}

export interface RouterConfig {
  baseUrl: string;
  grpcPort: number;
  strongModel: string;
  weakModel: string;
  routers: string[];
  routerConfig: Record<string, unknown>;
  eva: EvaConfig;
  autostart: boolean;
}

function defaultRouterConfig(): RouterConfig {
  return {
    baseUrl: "http://localhost:6060",
    grpcPort: 6061,
    strongModel: "",
    weakModel: "",
    routers: [],
    routerConfig: {},
    eva: { contracts: [], enforce: false },
    autostart: false,
  };
}

/**
 * Extract and validate RouteLLM configuration from provider extras.
 * Environment variables override all other sources.
 */
export function parseRouterConfig(
  extras?: Record<string, unknown>
): RouterConfig {
  const cfg = defaultRouterConfig();

  if (extras) {
    const raw = extras["routellm"];
    if (raw !== undefined) {
      if (typeof raw !== "object" || raw === null || Array.isArray(raw)) {
        throw new LLMError(
          `routellm: expected map, got ${typeof raw}`
        );
      }
      const sub = raw as Record<string, unknown>;

      if (typeof sub["base_url"] === "string") cfg.baseUrl = sub["base_url"];
      if (typeof sub["grpc_port"] === "number") cfg.grpcPort = sub["grpc_port"];
      if (typeof sub["strong_model"] === "string") {
        cfg.strongModel = sub["strong_model"];
      }
      if (typeof sub["weak_model"] === "string") {
        cfg.weakModel = sub["weak_model"];
      }
      if (Array.isArray(sub["routers"])) {
        cfg.routers = sub["routers"].filter(
          (r): r is string => typeof r === "string"
        );
      }
      if (
        typeof sub["router_config"] === "object" &&
        sub["router_config"] !== null &&
        !Array.isArray(sub["router_config"])
      ) {
        cfg.routerConfig = sub["router_config"] as Record<string, unknown>;
      }
      if (typeof sub["eva"] === "object" && sub["eva"] !== null) {
        const e = sub["eva"] as Record<string, unknown>;
        if (Array.isArray(e["contracts"])) {
          cfg.eva.contracts = e["contracts"].filter(
            (c): c is string => typeof c === "string"
          );
        }
        if (typeof e["enforce"] === "boolean") cfg.eva.enforce = e["enforce"];
      }
      if (typeof sub["autostart"] === "boolean") {
        cfg.autostart = sub["autostart"];
      }
    }
  }

  // Environment variable overrides (highest precedence).
  const envBaseUrl = process.env["ROUTELLM_BASE_URL"];
  if (envBaseUrl) cfg.baseUrl = envBaseUrl;

  const envStrong = process.env["ROUTELLM_STRONG_MODEL"];
  if (envStrong) cfg.strongModel = envStrong;

  const envWeak = process.env["ROUTELLM_WEAK_MODEL"];
  if (envWeak) cfg.weakModel = envWeak;

  const envRouters = process.env["ROUTELLM_ROUTERS"];
  if (envRouters) {
    cfg.routers = envRouters
      .split(",")
      .map((r) => r.trim())
      .filter(Boolean);
  }

  return cfg;
}

// ─── Model field parsing ──────────────────────────────────────────────────────

export interface ParsedModel {
  routerName: string;
  threshold: number;
}

/**
 * Split a model string of the form `router_name:threshold` into its
 * components.
 */
export function parseModelField(model: string): ParsedModel {
  if (!model) {
    throw new LLMError(
      "model field is required (format: router_name:threshold)"
    );
  }

  const idx = model.indexOf(":");
  if (idx < 0) {
    throw new LLMError(
      `invalid model "${model}": expected format ` +
        "router_name:threshold (e.g. mf:0.7)"
    );
  }

  const routerName = model.slice(0, idx);
  const rawThreshold = model.slice(idx + 1);

  if (!routerName || !rawThreshold) {
    throw new LLMError(
      `invalid model "${model}": expected format ` +
        "router_name:threshold (e.g. mf:0.7)"
    );
  }

  const threshold = Number(rawThreshold);
  if (Number.isNaN(threshold)) {
    throw new LLMError(`invalid threshold "${rawThreshold}": not a number`);
  }

  return { routerName, threshold };
}

// ─── Adapter ──────────────────────────────────────────────────────────────────

/**
 * RouteLLM adapter implementing Completer and Streamer.
 *
 * Delegates to an OpenAI-compatible HTTP endpoint on the RouteLLM
 * server using the standard fetch API.
 */
export class RouteLLMAdapter implements Completer, Streamer {
  readonly config: RouterConfig;
  readonly routerName: string;
  readonly threshold: number;

  private readonly _baseUrl: string;
  private readonly _serverModel: string;
  private readonly _apiKey: string;

  constructor(cfg: ResolvedConfig) {
    this.config = parseRouterConfig(cfg.provider.extras);

    const model = cfg.provider.model || cfg.uri.model;
    const parsed = parseModelField(model);
    this.routerName = parsed.routerName;
    this.threshold = parsed.threshold;

    if (this.threshold < 0 || this.threshold > 1) {
      throw new LLMError(
        `routellm: threshold ${this.threshold} out of range [0, 1]`
      );
    }

    // Build server model name: router-[name]-[threshold]
    this._serverModel = `router-${this.routerName}-${this.threshold}`;

    // Resolve base URL: config > provider > default.
    let baseUrl = this.config.baseUrl;
    if (baseUrl === "http://localhost:6060" && cfg.provider.baseURL) {
      baseUrl = cfg.provider.baseURL;
    }
    this._baseUrl = baseUrl;
    this._apiKey = cfg.provider.apiKey ?? "";
  }

  async complete(req: Request): Promise<Response> {
    const body = await this._buildBody(req, false);
    const resp = await this._post("/v1/chat/completions", body);

    const data = (await resp.json()) as OpenAIChatResponse;
    const choice = data.choices?.[0];
    const content = choice?.message?.content ?? "";
    const finishReason = choice?.finish_reason ?? "";

    const usage = data.usage
      ? {
          promptTokens: data.usage.prompt_tokens ?? 0,
          completionTokens: data.usage.completion_tokens ?? 0,
          totalTokens: data.usage.total_tokens ?? 0,
        }
      : undefined;

    return { content, role: "assistant", usage, finishReason };
  }

  async *stream(req: Request): AsyncGenerator<Token> {
    const body = await this._buildBody(req, true);
    const resp = await this._post("/v1/chat/completions", body);

    if (!resp.body) {
      throw new LLMError("routellm: streaming response has no body");
    }

    const reader = resp.body.getReader();
    const decoder = new TextDecoder();
    let buf = "";

    try {
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;

        buf += decoder.decode(value, { stream: true });
        const lines = buf.split("\n");
        buf = lines.pop() ?? "";

        for (const line of lines) {
          const trimmed = line.trim();
          if (!trimmed || !trimmed.startsWith("data: ")) continue;
          const payload = trimmed.slice(6);
          if (payload === "[DONE]") {
            yield { content: "", done: true };
            return;
          }

          const chunk = JSON.parse(payload) as OpenAIStreamChunk;
          const delta = chunk.choices?.[0]?.delta;
          const content = delta?.content ?? "";
          const isDone = chunk.choices?.[0]?.finish_reason != null;
          yield { content, done: isDone };
        }
      }
    } finally {
      reader.releaseLock();
    }
  }

  close(): void {
    // No persistent resources to release.
  }

  // ── private ────────────────────────────────────────────────────

  private async _buildBody(
    req: Request,
    stream: boolean
  ): Promise<Record<string, unknown>> {
    const messages: Record<string, unknown>[] = [];
    for (const m of req.messages) messages.push(await mapMessage(m));
    const body: Record<string, unknown> = {
      model: this._serverModel,
      messages,
      stream,
    };
    if (req.temperature !== undefined) body["temperature"] = req.temperature;
    if (req.maxTokens !== undefined) body["max_tokens"] = req.maxTokens;
    if (req.stopSequences?.length) body["stop"] = req.stopSequences;
    return body;
  }

  private async _post(
    path: string,
    body: Record<string, unknown>
  ): Promise<globalThis.Response> {
    const url = `${this._baseUrl}${path}`;
    const headers: Record<string, string> = {
      "Content-Type": "application/json",
    };
    if (this._apiKey) {
      headers["Authorization"] = `Bearer ${this._apiKey}`;
    }

    const resp = await fetch(url, {
      method: "POST",
      headers,
      body: JSON.stringify(body),
    });

    if (!resp.ok) {
      const text = await resp.text().catch(() => "");
      throw new LLMError(
        `routellm: HTTP ${resp.status} from ${url}: ${text}`
      );
    }

    return resp;
  }
}

// ─── Message mapping (OpenAI-compatible wire) ─────────────────────────────────

/**
 * Maps one message onto the OpenAI chat wire, mirroring the Go openai
 * adapter: assistant `toolCalls` become `tool_calls`, a role `tool`
 * result carries `tool_call_id`, and `parts` become a user content
 * array. Linkage or parts the wire cannot carry throw instead of being
 * sent as plain text. Messages without them map to `{role, content}`.
 */
async function mapMessage(m: Message): Promise<Record<string, unknown>> {
  if (m.role === "tool" && !m.toolCallId) {
    throw new LLMError("routellm: tool result needs toolCallId");
  }
  checkToolLinkage(m);

  if (m.role === "tool") {
    return { role: "tool", content: m.content, tool_call_id: m.toolCallId };
  }

  if (m.toolCalls?.length) {
    if (m.parts?.length) {
      throw new LLMError(
        "routellm: assistant tool calls do not support content parts"
      );
    }
    const out: Record<string, unknown> = { role: "assistant" };
    if (m.content) out["content"] = m.content;
    out["tool_calls"] = m.toolCalls.map((tc) => {
      if (!tc.id) {
        throw new LLMError(`routellm: tool call "${tc.name}" needs an id`);
      }
      return {
        id: tc.id,
        type: "function",
        function: {
          name: tc.name,
          arguments: encodeArguments(tc.id, tc.arguments),
        },
      };
    });
    return out;
  }

  if (!m.parts?.length) return { role: m.role, content: m.content };

  if (m.role !== "" && m.role !== "user") {
    throw new LLMError(
      `routellm: message role "${m.role}" does not support content parts`
    );
  }
  const content: Record<string, unknown>[] = [];
  for (const p of m.parts) content.push(await mapPart(p));
  return { role: "user", content };
}

/** Tool-call arguments as the JSON string the wire expects. */
function encodeArguments(id: string, args: unknown): string {
  if (args === undefined || args === null) return "{}";
  if (typeof args !== "string") return JSON.stringify(args);
  try {
    JSON.parse(args);
  } catch {
    throw new LLMError(
      `routellm: tool call "${id}" arguments are not valid JSON`
    );
  }
  return args;
}

async function mapPart(p: ContentPart): Promise<Record<string, unknown>> {
  switch (p.type) {
    case "text":
      return { type: "text", text: p.text ?? "" };

    case "image": {
      const mime = p.mimeType || p.source?.mimeType() || "";
      if (mime === "application/pdf") {
        return { type: "file", file: { file_data: await readBase64(p) } };
      }
      const url = p.source?.url();
      if (url) return { type: "image_url", image_url: { url } };
      if (!mime) {
        throw new LLMError("routellm: image part needs a MIME type or URL");
      }
      return {
        type: "image_url",
        image_url: { url: `data:${mime};base64,${await readBase64(p)}` },
      };
    }

    default:
      throw new LLMError(`routellm: unsupported modality "${p.type}"`);
  }
}

async function readBase64(p: ContentPart): Promise<string> {
  if (!p.source) throw new LLMError("routellm: nil media source");
  return Buffer.from(await p.source.read()).toString("base64");
}

// ─── OpenAI-compatible response types (internal) ──────────────────────────────

interface OpenAIChatResponse {
  choices?: {
    message?: { content?: string; role?: string };
    finish_reason?: string;
  }[];
  usage?: {
    prompt_tokens?: number;
    completion_tokens?: number;
    total_tokens?: number;
  };
}

interface OpenAIStreamChunk {
  choices?: {
    delta?: { content?: string; role?: string };
    finish_reason?: string | null;
  }[];
}

// ─── Registration ─────────────────────────────────────────────────────────────

function routellmFactory(cfg: ResolvedConfig): Provider {
  const adapter = new RouteLLMAdapter(cfg);
  return {
    complete: (req) => adapter.complete(req),
    stream: (req) => adapter.stream(req),
    close: () => adapter.close(),
  };
}

register("routellm", routellmFactory);
