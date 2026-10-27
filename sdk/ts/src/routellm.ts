/**
 * @packageDocumentation
 *
 * RouteLLM adapter — registers the `routellm` URI scheme.
 *
 * URI format: `routellm://router_name:threshold` (e.g. `routellm://mf:0.7`).
 *
 * Delegates to the openai adapter (`@hop-top/kit/openai`) pointed
 * at the RouteLLM server's OpenAI-compatible endpoint, translating the
 * model field into the server's `router-[name]-[threshold]` format.
 * Importing this module registers the openai schemes too.
 */

import {
  type Completer,
  type Streamer,
  type ToolCaller,
  type ToolStreamer,
  type ToolDef,
  type ToolResponse,
  type ToolStream,
  type Request,
  type Response,
  type ResolvedConfig,
  type Provider,
  LLMError,
  TOOL_NAMES_PARAM,
  register,
  toolNameModeOf,
} from "./llm";
import { OpenAIAdapter, apiRootV1 } from "./llm-openai";

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
 * Environment variables (`ROUTELLM_BASE_URL`, `ROUTELLM_STRONG_MODEL`,
 * `ROUTELLM_WEAK_MODEL`, `ROUTELLM_ROUTERS`) override all other
 * sources; with `opts.hermetic` none is read.
 */
export function parseRouterConfig(
  extras?: Record<string, unknown>,
  opts: { hermetic?: boolean } = {}
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

  if (opts.hermetic) return cfg;

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
 * RouteLLM adapter: complete, stream, callWithTools and streamWithTools.
 *
 * Delegates to an inner {@link OpenAIAdapter} pointed at the RouteLLM
 * server's OpenAI-compatible `/v1` API, so tool calling, tool names
 * (openai rule, `?tool_names=`), size limits, retries and the
 * credential guard are the openai adapter's.
 */
export class RouteLLMAdapter
  implements Completer, Streamer, ToolCaller, ToolStreamer
{
  readonly config: RouterConfig;
  readonly routerName: string;
  readonly threshold: number;

  private readonly inner: OpenAIAdapter;

  constructor(cfg: ResolvedConfig) {
    this.config = parseRouterConfig(cfg.provider.extras, { hermetic: cfg.hermetic });
    // The server speaks the OpenAI wire: its tool-name rule applies.
    // Checked here so an unknown mode fails construction.
    toolNameModeOf(cfg.provider.params);

    const model = cfg.provider.model || cfg.uri.model;
    const parsed = parseModelField(model);
    this.routerName = parsed.routerName;
    this.threshold = parsed.threshold;

    if (this.threshold < 0 || this.threshold > 1) {
      throw new LLMError(
        `routellm: threshold ${this.threshold} out of range [0, 1]`
      );
    }

    // Resolve base URL: config > provider > default.
    let baseUrl = this.config.baseUrl;
    if (baseUrl === "http://localhost:6060" && cfg.provider.baseURL) {
      baseUrl = cfg.provider.baseURL;
    }

    // The inner adapter takes the params it owns.
    const params: Record<string, string> = {};
    for (const k of [TOOL_NAMES_PARAM, "max_body_bytes", "max_event_bytes"]) {
      const v = cfg.provider.params?.[k];
      if (v !== undefined) params[k] = v;
    }
    this.inner = new OpenAIAdapter(
      {
        uri: { scheme: "routellm", model },
        provider: {
          apiKey: cfg.provider.apiKey,
          baseURL: apiRootV1(baseUrl),
          // Server model name: router-[name]-[threshold]
          model: `router-${this.routerName}-${this.threshold}`,
          params,
        },
        fallbacks: [],
        hermetic: cfg.hermetic,
      },
      { label: "routellm" }
    );
  }

  complete(req: Request): Promise<Response> {
    return this.inner.complete(req);
  }

  stream(req: Request): ToolStream {
    return this.inner.stream(req);
  }

  callWithTools(req: Request, tools: ToolDef[]): Promise<ToolResponse> {
    return this.inner.callWithTools(req, tools);
  }

  streamWithTools(req: Request, tools: ToolDef[]): ToolStream {
    return this.inner.streamWithTools(req, tools);
  }

  close(): void {
    this.inner.close();
  }
}

// ─── Registration ─────────────────────────────────────────────────────────────

function routellmFactory(cfg: ResolvedConfig): Provider {
  const adapter = new RouteLLMAdapter(cfg);
  return {
    complete: (req) => adapter.complete(req),
    stream: (req) => adapter.stream(req),
    callWithTools: (req, tools) => adapter.callWithTools(req, tools),
    streamWithTools: (req, tools) => adapter.streamWithTools(req, tools),
    close: () => adapter.close(),
  };
}

register("routellm", routellmFactory);
