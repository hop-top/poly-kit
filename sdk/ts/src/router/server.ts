/**
 * Hono HTTP handler for RouteLLM.
 *
 * Exposes /v1/chat/completions (routes + proxies to upstream LLM)
 * and /health for liveness checks.
 *
 * Port of Python's openai_server.py.
 */

import { Hono } from "hono";
import { Controller } from "./controller";
import { RoutingError } from "./router";

// ─── Request / response types (OpenAI-compatible) ────────────────────────────

export interface ChatMessage {
  role: string;
  content: string;
}

export interface ChatCompletionRequest {
  model: string;
  messages: ChatMessage[];
  stream?: boolean;
  temperature?: number;
  max_tokens?: number;
  top_p?: number;
  stop?: string | string[];
  n?: number;
  [key: string]: unknown;
}

export interface ChatCompletionChoice {
  index: number;
  message: ChatMessage;
  finish_reason: string | null;
}

export interface UsageInfo {
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
}

export interface ChatCompletionResponse {
  id: string;
  object: string;
  created: number;
  model: string;
  choices: ChatCompletionChoice[];
  usage: UsageInfo;
}

// ─── Upstream proxy type ─────────────────────────────────────────────────────

/**
 * Function that forwards a completion request to the upstream LLM.
 *
 * Receives the request body with `model` already set to the routed
 * model name. Returns the upstream response to relay to the client.
 */
export type UpstreamFn = (
  body: ChatCompletionRequest,
) => Promise<ChatCompletionResponse>;

// ─── App factory ─────────────────────────────────────────────────────────────

export interface ServerOptions {
  /** The controller with registered routers. */
  controller: Controller;
  /** Function to forward requests to the upstream LLM. */
  upstream: UpstreamFn;
}

/**
 * Create a Hono app with RouteLLM endpoints.
 *
 * Routes:
 *   POST /v1/chat/completions — route + proxy to upstream LLM.
 *   GET  /health              — liveness check.
 */
export function createApp(opts: ServerOptions): Hono {
  const app = new Hono();
  const { controller, upstream } = opts;

  // ── Health ──────────────────────────────────────────────────────
  app.get("/health", (c) => {
    return c.json({ status: "online" });
  });

  // ── Chat Completions ───────────────────────────────────────────
  app.post("/v1/chat/completions", async (c) => {
    let body: ChatCompletionRequest;
    try {
      body = await c.req.json<ChatCompletionRequest>();
    } catch {
      return c.json({ object: "error", message: "invalid JSON" }, 400);
    }

    if (!body.model || !body.messages?.length) {
      return c.json(
        { object: "error", message: "model and messages required" },
        400,
      );
    }

    // Extract last user message for routing.
    const userMsg = [...body.messages].reverse().find(
      (m: { role: string }) => m.role === "user",
    );
    const prompt = userMsg?.content ?? body.messages[body.messages.length - 1]?.content ?? "";

    let routedModel: string;
    try {
      routedModel = await controller.routeByModelName(prompt, body.model);
    } catch (err) {
      if (err instanceof RoutingError) {
        return c.json({ object: "error", message: err.message }, 400);
      }
      throw err;
    }

    // Replace model with routed model and forward.
    const upstreamBody: ChatCompletionRequest = {
      ...body,
      model: routedModel,
    };

    const result = await upstream(upstreamBody);
    return c.json(result);
  });

  return app;
}
