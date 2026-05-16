import { describe, it, expect, vi } from "vitest";
import { createApp } from "./server";
import { Controller } from "./controller";
import type { Router } from "./router";
import type {
  ChatCompletionRequest,
  ChatCompletionResponse,
  UpstreamFn,
} from "./server";

// ─── Helpers ─────────────────────────────────────────────────────────────────

function fixedRouter(score: number): Router {
  return { score: vi.fn(async () => score) };
}

function mockUpstream(): UpstreamFn {
  return vi.fn(async (body: ChatCompletionRequest) => ({
    id: "chatcmpl-test",
    object: "chat.completion",
    created: 1000,
    model: body.model,
    choices: [
      {
        index: 0,
        message: { role: "assistant", content: `routed to ${body.model}` },
        finish_reason: "stop",
      },
    ],
    usage: { prompt_tokens: 10, completion_tokens: 5, total_tokens: 15 },
  }));
}

function makeApp(
  score: number,
  upstream?: UpstreamFn,
) {
  const controller = new Controller({
    strongModel: "gpt-4",
    weakModel: "gpt-3.5",
    routers: { mf: fixedRouter(score) },
  });
  return createApp({
    controller,
    upstream: upstream ?? mockUpstream(),
  });
}

// ─── Tests ───────────────────────────────────────────────────────────────────

describe("server /health", () => {
  it("returns 200 with status online", async () => {
    const app = makeApp(0.5);
    const res = await app.request("/health");
    expect(res.status).toBe(200);
    const json = await res.json();
    expect(json.status).toBe("online");
  });
});

describe("server /v1/chat/completions", () => {
  it("routes to strong model when score >= threshold", async () => {
    const upstream = mockUpstream();
    const app = makeApp(0.9, upstream); // score=0.9 >= threshold=0.5

    const body: ChatCompletionRequest = {
      model: "router-mf-0.5",
      messages: [{ role: "user", content: "hello" }],
    };

    const res = await app.request("/v1/chat/completions", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });

    expect(res.status).toBe(200);
    const json = (await res.json()) as ChatCompletionResponse;
    expect(json.model).toBe("gpt-4");
    expect(upstream).toHaveBeenCalled();
  });

  it("routes to weak model when score < threshold", async () => {
    const upstream = mockUpstream();
    const app = makeApp(0.1, upstream); // score=0.1 < threshold=0.5

    const body: ChatCompletionRequest = {
      model: "router-mf-0.5",
      messages: [{ role: "user", content: "hello" }],
    };

    const res = await app.request("/v1/chat/completions", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });

    expect(res.status).toBe(200);
    const json = (await res.json()) as ChatCompletionResponse;
    expect(json.model).toBe("gpt-3.5");
  });

  it("returns 400 for invalid model format", async () => {
    const app = makeApp(0.5);

    const res = await app.request("/v1/chat/completions", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        model: "bad-model",
        messages: [{ role: "user", content: "hi" }],
      }),
    });

    expect(res.status).toBe(400);
    const json = await res.json();
    expect(json.object).toBe("error");
  });

  it("returns 400 for unknown router", async () => {
    const app = makeApp(0.5);

    const res = await app.request("/v1/chat/completions", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        model: "router-unknown-0.5",
        messages: [{ role: "user", content: "hi" }],
      }),
    });

    expect(res.status).toBe(400);
  });

  it("returns 400 for missing model/messages", async () => {
    const app = makeApp(0.5);

    const res = await app.request("/v1/chat/completions", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({}),
    });

    expect(res.status).toBe(400);
    const json = await res.json();
    expect(json.message).toContain("required");
  });

  it("returns 400 for invalid JSON body", async () => {
    const app = makeApp(0.5);

    const res = await app.request("/v1/chat/completions", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: "not json",
    });

    expect(res.status).toBe(400);
  });

  it("uses last message for routing", async () => {
    const router: Router = {
      score: vi.fn(async (prompt: string) => {
        // Return high score only for "route me".
        return prompt === "route me" ? 0.9 : 0.1;
      }),
    };
    const upstream = mockUpstream();
    const controller = new Controller({
      strongModel: "gpt-4",
      weakModel: "gpt-3.5",
      routers: { mf: router },
    });
    const app = createApp({ controller, upstream });

    const res = await app.request("/v1/chat/completions", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        model: "router-mf-0.5",
        messages: [
          { role: "user", content: "first" },
          { role: "user", content: "route me" },
        ],
      }),
    });

    expect(res.status).toBe(200);
    const json = (await res.json()) as ChatCompletionResponse;
    expect(json.model).toBe("gpt-4");
    expect(router.score).toHaveBeenCalledWith("route me");
  });
});
