/**
 * @packageDocumentation
 *
 * RouteLLM adapter — registers the `routellm` URI scheme.
 *
 * The model field takes either shape a RouteLLM server advertises on
 * `/v1/models`, as in Go:
 *
 * - `router_name:threshold` — `routellm://mf:0.7`, sent as the server's
 *   `router-[name]-[threshold]` model. The client picks both.
 * - tier name — `routellm://private`, sent verbatim. The server resolves
 *   router and threshold from its own tier config.
 *
 * Delegates to the openai adapter (`@hop-top/kit/openai`) pointed
 * at the RouteLLM server's OpenAI-compatible endpoint.
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

/**
 * The model field classified. `isTier` selects `tier`; otherwise
 * `routerName` and `threshold` hold the client-selected pair. The
 * unused shape stays at `""` / `0`.
 */
export interface ParsedModel {
  isTier: boolean;
  tier: string;
  routerName: string;
  threshold: number;
}

const MODEL_REQUIRED =
  "model field is required (router_name:threshold or tier name)";

const DECIMAL_FLOAT = /^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$/;
const HEX_FLOAT =
  /^([+-]?)0[xX]([0-9a-fA-F]*)(?:\.([0-9a-fA-F]*))?[pP]([+-]?\d+)$/;

/**
 * Go's underscore rule for float literals: an underscore must sit
 * between digits, or between a base prefix and a digit.
 */
function underscoreOK(text: string): boolean {
  let s = text;
  if (s[0] === "+" || s[0] === "-") s = s.slice(1);
  let saw = "^"; // ^ start, 0 digit or prefix, _ underscore, ! other
  let i = 0;
  let hex = false;
  const p = s[1]?.toLowerCase();
  if (s.length >= 2 && s[0] === "0" && (p === "b" || p === "o" || p === "x")) {
    i = 2;
    saw = "0";
    hex = p === "x";
  }
  for (; i < s.length; i++) {
    const c = s[i]!;
    if ((c >= "0" && c <= "9") || (hex && /[a-fA-F]/.test(c))) {
      saw = "0";
      continue;
    }
    if (c === "_") {
      if (saw !== "0") return false;
      saw = "_";
      continue;
    }
    if (saw === "_") return false;
    saw = "!";
  }
  return saw !== "_";
}

/**
 * A finite float in the syntax Go's `strconv.ParseFloat` accepts
 * (decimal or hex mantissa, underscores between digits), else
 * `undefined`. Infinities, NaN and out-of-range values are not.
 */
function parseFiniteFloat(text: string): number | undefined {
  if (text.includes("_")) {
    if (!underscoreOK(text)) return undefined;
    text = text.replace(/_/g, "");
  }
  let v: number;
  if (DECIMAL_FLOAT.test(text)) {
    v = Number(text);
  } else {
    const m = HEX_FLOAT.exec(text);
    if (!m) return undefined;
    const [, sign, int = "", frac = "", exp = "0"] = m;
    if (!int && !frac) return undefined;
    v = parseInt(int + frac || "0", 16) * 2 ** (Number(exp) - 4 * frac.length);
    if (sign === "-") v = -v;
  }
  return Number.isFinite(v) ? v : undefined;
}

/**
 * Format a float as Go's `strconv.FormatFloat(f, 'f', -1, 64)`: the
 * shortest round-tripping digits, never an exponent, `-0` kept.
 */
function formatFloat(v: number): string {
  if (Object.is(v, -0)) return "-0";
  const sign = v < 0 ? "-" : "";
  const repr = String(Math.abs(v));
  const e = repr.indexOf("e");
  if (e < 0) return sign + repr;
  const exp = Number(repr.slice(e + 1));
  const digits = repr.slice(0, e).replace(".", "");
  const point = 1 + exp; // position of the decimal point in digits
  if (point <= 0) return `${sign}0.${"0".repeat(-point)}${digits}`;
  if (point >= digits.length) {
    return sign + digits + "0".repeat(point - digits.length);
  }
  return `${sign}${digits.slice(0, point)}.${digits.slice(point)}`;
}

/**
 * Classify the model field as a `router_name:threshold` pair or a tier
 * name, exactly as Go does.
 *
 * The shapes are told apart by the text after the last colon: when it
 * parses as a finite float (Go `ParseFloat` syntax) the model is a
 * router pair, else a tier sent verbatim — so a tier name may contain
 * colons and `mf:abc` is a tier. A leading or trailing colon leaves a
 * tier. The threshold range is checked by the adapter, not here; an
 * unknown tier fails at the server.
 */
export function parseModelField(model: string): ParsedModel {
  if (!model) throw new LLMError(MODEL_REQUIRED);

  const idx = model.lastIndexOf(":");
  if (idx > 0 && idx < model.length - 1) {
    const threshold = parseFiniteFloat(model.slice(idx + 1));
    if (threshold !== undefined) {
      return {
        isTier: false,
        tier: "",
        routerName: model.slice(0, idx),
        threshold,
      };
    }
  }

  return { isTier: true, tier: model, routerName: "", threshold: 0 };
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
  /** Client-selected router; `""` for a tier. */
  readonly routerName: string;
  /** Client-selected threshold; `0` for a tier (see {@link isTier}). */
  readonly threshold: number;
  /** Server-resolved tier name; `""` for a router pair. */
  readonly tier: string;

  private readonly inner: OpenAIAdapter;

  constructor(cfg: ResolvedConfig) {
    this.config = parseRouterConfig(cfg.provider.extras, { hermetic: cfg.hermetic });
    // The server speaks the OpenAI wire: its tool-name rule applies.
    // Checked here so an unknown mode fails construction.
    toolNameModeOf(cfg.provider.params);

    const model = cfg.provider.model || cfg.uri.model;
    if (!model) throw new LLMError(`routellm: ${MODEL_REQUIRED}`);
    const parsed = parseModelField(model);
    this.routerName = parsed.routerName;
    this.threshold = parsed.threshold;
    this.tier = parsed.tier;

    // A tier travels verbatim; a router pair becomes
    // router-[name]-[threshold].
    let serverModel = parsed.tier;
    if (!parsed.isTier) {
      if (this.threshold < 0 || this.threshold > 1) {
        throw new LLMError(
          `routellm: threshold ${this.threshold.toFixed(4)} out of range [0, 1]`
        );
      }
      serverModel = `router-${this.routerName}-${formatFloat(this.threshold)}`;
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
          model: serverModel,
          params,
        },
        fallbacks: [],
        hermetic: cfg.hermetic,
      },
      { label: "routellm" }
    );
  }

  /**
   * Whether the adapter targets a server-resolved tier rather than a
   * client-selected pair; use it instead of `threshold === 0`, which is
   * also a valid client threshold.
   */
  isTier(): boolean {
    return this.tier !== "";
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
