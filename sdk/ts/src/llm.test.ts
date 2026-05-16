/**
 * Tests for LLM module (llm.ts).
 * Covers: parseURI, loadConfig, registry, Client, fallback, hooks, errors.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  parseURI,
  loadConfig,
  register,
  resolve,
  clearRegistry,
  createLLM,
  LLMError,
  ProviderNotFoundError,
  CapabilityNotSupportedError,
  AuthError,
  RateLimitError,
  ModelError,
  FallbackExhaustedError,
  isFallbackable,
  type Request,
  type Response,
  type Provider,
  type Token,
  type Factory,
} from "./llm";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function mockProvider(overrides: Partial<Provider> = {}): Provider {
  return {
    close: vi.fn(),
    ...overrides,
  };
}

function mockCompleteProvider(
  response: Response
): Provider {
  return {
    complete: vi.fn().mockResolvedValue(response),
    close: vi.fn(),
  };
}

function req(content = "hello"): Request {
  return { messages: [{ role: "user", content }] };
}

// ---------------------------------------------------------------------------
// parseURI
// ---------------------------------------------------------------------------

describe("parseURI", () => {
  it("parses basic scheme://model", () => {
    const uri = parseURI("openai://gpt-4");
    expect(uri.scheme).toBe("openai");
    expect(uri.model).toBe("gpt-4");
    expect(uri.host).toBeUndefined();
    expect(uri.params).toBeUndefined();
  });

  it("parses scheme with params", () => {
    const uri = parseURI("openai://gpt-4?temperature=0.7&max_tokens=100");
    expect(uri.scheme).toBe("openai");
    expect(uri.model).toBe("gpt-4");
    expect(uri.params).toEqual({ temperature: "0.7", max_tokens: "100" });
  });

  it("parses with host:port", () => {
    const uri = parseURI("ollama://localhost:11434/llama3");
    expect(uri.scheme).toBe("ollama");
    expect(uri.host).toBe("localhost:11434");
    expect(uri.model).toBe("llama3");
  });

  it("handles slashes in model name", () => {
    const uri = parseURI("openai://org/model-name");
    expect(uri.scheme).toBe("openai");
    expect(uri.model).toBe("org/model-name");
  });

  it("handles host + slashes in model", () => {
    const uri = parseURI("ollama://myhost:8080/ns/model-v2");
    expect(uri.scheme).toBe("ollama");
    expect(uri.host).toBe("myhost:8080");
    expect(uri.model).toBe("ns/model-v2");
  });

  it("throws on missing scheme", () => {
    expect(() => parseURI("gpt-4")).toThrow();
  });

  it("throws on empty model", () => {
    expect(() => parseURI("openai://")).toThrow();
  });

  it("throws on empty string", () => {
    expect(() => parseURI("")).toThrow();
  });
});

// ---------------------------------------------------------------------------
// loadConfig
// ---------------------------------------------------------------------------

describe("loadConfig", () => {
  const saved: Record<string, string | undefined> = {};
  const envKeys = [
    "LLM_PROVIDER",
    "LLM_API_KEY",
    "LLM_BASE_URL",
    "LLM_FALLBACK",
  ];

  beforeEach(() => {
    for (const k of envKeys) {
      saved[k] = process.env[k];
      delete process.env[k];
    }
  });

  afterEach(() => {
    for (const k of envKeys) {
      if (saved[k] === undefined) delete process.env[k];
      else process.env[k] = saved[k];
    }
  });

  it("loads from URI string", () => {
    const cfg = loadConfig("openai://gpt-4");
    expect(cfg.uri.scheme).toBe("openai");
    expect(cfg.uri.model).toBe("gpt-4");
    expect(cfg.provider.model).toBe("gpt-4");
  });

  it("applies env var overrides", () => {
    process.env["LLM_API_KEY"] = "sk-test";
    process.env["LLM_BASE_URL"] = "https://custom.api.com";
    process.env["LLM_FALLBACK"] = "anthropic://claude-3,ollama://llama3";

    const cfg = loadConfig("openai://gpt-4");
    expect(cfg.provider.apiKey).toBe("sk-test");
    expect(cfg.provider.baseURL).toBe("https://custom.api.com");
    expect(cfg.fallbacks).toEqual([
      "anthropic://claude-3",
      "ollama://llama3",
    ]);
  });

  it("LLM_PROVIDER env is used as full default URI when no arg", () => {
    process.env["LLM_PROVIDER"] = "anthropic://claude-3";
    const cfg = loadConfig();
    expect(cfg.uri.scheme).toBe("anthropic");
    expect(cfg.uri.model).toBe("claude-3");
  });

  it("LLM_PROVIDER env is ignored when URI arg is provided", () => {
    process.env["LLM_PROVIDER"] = "anthropic://claude-3";
    const cfg = loadConfig("openai://gpt-4");
    expect(cfg.uri.scheme).toBe("openai");
    expect(cfg.uri.model).toBe("gpt-4");
  });

  it("throws when no URI and no LLM_PROVIDER env", () => {
    expect(() => loadConfig()).toThrow(/no LLM URI provided/);
  });

  it("passes URI params to provider params", () => {
    const cfg = loadConfig("openai://gpt-4?temperature=0.5");
    expect(cfg.provider.params).toEqual({ temperature: "0.5" });
  });

  it("extracts api_key from URI params", () => {
    const cfg = loadConfig("openai://gpt-4?api_key=sk-test123");
    expect(cfg.provider.apiKey).toBe("sk-test123");
  });

  it("extracts base_url from URI params", () => {
    const cfg = loadConfig(
      "openai://gpt-4?base_url=https%3A%2F%2Fcustom.api.com"
    );
    expect(cfg.provider.baseURL).toBe("https://custom.api.com");
  });

  it("derives baseURL from uri.host", () => {
    const cfg = loadConfig("ollama://localhost:11434/llama3");
    expect(cfg.provider.baseURL).toBe("http://localhost:11434");
  });

  it("env LLM_API_KEY overrides URI api_key param", () => {
    process.env["LLM_API_KEY"] = "env-key";
    const cfg = loadConfig("openai://gpt-4?api_key=uri-key");
    expect(cfg.provider.apiKey).toBe("env-key");
  });

  it("env LLM_BASE_URL overrides host-derived baseURL", () => {
    process.env["LLM_BASE_URL"] = "https://override.com";
    const cfg = loadConfig("ollama://localhost:11434/llama3");
    expect(cfg.provider.baseURL).toBe("https://override.com");
  });
});

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

describe("registry", () => {
  beforeEach(() => clearRegistry());

  it("register + resolve returns a provider", () => {
    const factory: Factory = (_cfg) =>
      mockCompleteProvider({ content: "ok", role: "assistant" });
    register("test", factory);
    const provider = resolve("test://model-1");
    expect(provider).toBeDefined();
    expect(provider.complete).toBeDefined();
  });

  it("resolve throws ProviderNotFoundError for unknown scheme", () => {
    expect(() => resolve("unknown://model")).toThrow(ProviderNotFoundError);
  });

  it("duplicate register throws", () => {
    const factory: Factory = () => mockProvider();
    register("dup", factory);
    expect(() => register("dup", factory)).toThrow();
  });
});

// ---------------------------------------------------------------------------
// Client
// ---------------------------------------------------------------------------

describe("Client", () => {
  beforeEach(() => clearRegistry());

  it("complete delegates to provider", async () => {
    const resp: Response = {
      content: "world",
      role: "assistant",
      usage: { promptTokens: 5, completionTokens: 3, totalTokens: 8 },
    };
    register("mock", () => mockCompleteProvider(resp));

    const client = createLLM("mock://test-model");
    const result = await client.complete(req());
    expect(result.content).toBe("world");
    expect(result.role).toBe("assistant");
  });

  it("stream throws CapabilityNotSupportedError when unsupported", async () => {
    register("mock", () => mockProvider());
    const client = createLLM("mock://m");
    await expect(async () => {
       
      for await (const _t of client.stream(req())) {
        /* noop */
      }
    }).rejects.toThrow(CapabilityNotSupportedError);
  });

  it("capabilities reflects provider methods", () => {
    register("mock", () =>
      mockProvider({
        complete: vi.fn(),
        stream: vi.fn() as unknown as Provider["stream"],
      })
    );
    const client = createLLM("mock://m");
    const caps = client.capabilities();
    expect(caps).toContain("complete");
    expect(caps).toContain("stream");
    expect(caps).not.toContain("callWithTools");
  });

  it("provider accessor returns the underlying provider", () => {
    const p = mockProvider();
    register("mock", () => p);
    const client = createLLM("mock://m");
    expect(client.provider()).toBe(p);
  });

  it("close delegates to provider", () => {
    const p = mockProvider();
    register("mock", () => p);
    const client = createLLM("mock://m");
    client.close();
    expect(p.close).toHaveBeenCalled();
  });

  it("close also closes fallback providers used during operation", async () => {
    const err5xx = new LLMError("down");
    err5xx.statusCode = 500;
    const primaryP = mockProvider({
      complete: vi.fn().mockRejectedValue(err5xx),
    });
    const backupP = mockCompleteProvider({
      content: "ok",
      role: "assistant",
    });

    register("pri", () => primaryP);
    register("bak", () => backupP);

    const client = createLLM("pri://m", { fallback: ["bak://m2"] });
    await client.complete(req());
    client.close();
    expect(primaryP.close).toHaveBeenCalled();
    expect(backupP.close).toHaveBeenCalled();
  });
});

// ---------------------------------------------------------------------------
// Fallback
// ---------------------------------------------------------------------------

describe("fallback", () => {
  beforeEach(() => clearRegistry());

  it("5xx triggers fallback to next provider", async () => {
    const err5xx = new LLMError("server error");
    err5xx.statusCode = 500;

    register("primary", () =>
      mockProvider({
        complete: vi.fn().mockRejectedValue(err5xx),
      })
    );
    register("backup", () =>
      mockCompleteProvider({ content: "backup-ok", role: "assistant" })
    );

    const client = createLLM("primary://m", {
      fallback: ["backup://fallback-model"],
    });
    const result = await client.complete(req());
    expect(result.content).toBe("backup-ok");
  });

  it("4xx does NOT trigger fallback", async () => {
    const err4xx = new LLMError("bad request");
    err4xx.statusCode = 400;

    register("primary", () =>
      mockProvider({ complete: vi.fn().mockRejectedValue(err4xx) })
    );
    register("backup", () =>
      mockCompleteProvider({ content: "should-not-reach", role: "assistant" })
    );

    const client = createLLM("primary://m", {
      fallback: ["backup://fb"],
    });
    await expect(client.complete(req())).rejects.toThrow("bad request");
  });

  it("capability mismatch skips to next provider", async () => {
    // Primary has no complete; backup does
    register("nocap", () => mockProvider());
    register("capable", () =>
      mockCompleteProvider({ content: "from-capable", role: "assistant" })
    );

    const client = createLLM("nocap://m", {
      fallback: ["capable://m2"],
    });
    const result = await client.complete(req());
    expect(result.content).toBe("from-capable");
  });

  it("stream falls back on provider failure", async () => {
    const err5xx = new LLMError("down");
    err5xx.statusCode = 500;

    async function* failStream(): AsyncIterable<Token> {
      throw err5xx;
    }
    async function* okStream(): AsyncIterable<Token> {
      yield { content: "streamed", done: true };
    }

    register("brokens", () =>
      mockProvider({ stream: () => failStream() as any })
    );
    register("goods", () =>
      mockProvider({ stream: () => okStream() as any })
    );

    const client = createLLM("brokens://m", {
      fallback: ["goods://m2"],
    });
    const tokens: Token[] = [];
    for await (const t of client.stream(req())) {
      tokens.push(t);
    }
    expect(tokens).toHaveLength(1);
    expect(tokens[0].content).toBe("streamed");
  });

  it("stream falls back when provider lacks stream capability", async () => {
    async function* okStream(): AsyncIterable<Token> {
      yield { content: "ok", done: true };
    }

    register("nostream", () => mockProvider());
    register("hasstream", () =>
      mockProvider({ stream: () => okStream() as any })
    );

    const client = createLLM("nostream://m", {
      fallback: ["hasstream://m2"],
    });
    const tokens: Token[] = [];
    for await (const t of client.stream(req())) {
      tokens.push(t);
    }
    expect(tokens[0].content).toBe("ok");
  });

  it("all providers exhausted throws FallbackExhaustedError", async () => {
    const err = new LLMError("down");
    err.statusCode = 503;

    register("a", () =>
      mockProvider({ complete: vi.fn().mockRejectedValue(err) })
    );
    register("b", () =>
      mockProvider({ complete: vi.fn().mockRejectedValue(err) })
    );

    const client = createLLM("a://m1", { fallback: ["b://m2"] });
    await expect(client.complete(req())).rejects.toThrow(
      FallbackExhaustedError
    );
  });
});

// ---------------------------------------------------------------------------
// Event hooks
// ---------------------------------------------------------------------------

describe("event hooks", () => {
  beforeEach(() => clearRegistry());

  it("onRequest fires before call", async () => {
    const onRequest = vi.fn();
    register("mock", () =>
      mockCompleteProvider({ content: "ok", role: "assistant" })
    );
    const client = createLLM("mock://m", { onRequest });
    await client.complete(req("ping"));
    expect(onRequest).toHaveBeenCalledTimes(1);
    expect(onRequest.mock.calls[0][0].messages[0].content).toBe("ping");
  });

  it("onResponse fires after call with duration", async () => {
    const onResponse = vi.fn();
    register("mock", () =>
      mockCompleteProvider({ content: "ok", role: "assistant" })
    );
    const client = createLLM("mock://m", { onResponse });
    await client.complete(req());
    expect(onResponse).toHaveBeenCalledTimes(1);
    expect(typeof onResponse.mock.calls[0][1]).toBe("number");
  });

  it("onError fires on failure", async () => {
    const onError = vi.fn();
    const err = new LLMError("boom");
    err.statusCode = 400;
    register("mock", () =>
      mockProvider({ complete: vi.fn().mockRejectedValue(err) })
    );
    const client = createLLM("mock://m", { onError });
    await expect(client.complete(req())).rejects.toThrow();
    expect(onError).toHaveBeenCalledTimes(1);
  });

  it("onResponse fires for callWithTools", async () => {
    const onResponse = vi.fn();
    register("mock", () =>
      mockProvider({
        callWithTools: vi
          .fn()
          .mockResolvedValue({ content: "ok", toolCalls: [] }),
      })
    );
    const client = createLLM("mock://m", { onResponse });
    await client.callWithTools(req(), []);
    expect(onResponse).toHaveBeenCalledTimes(1);
    expect(typeof onResponse.mock.calls[0][1]).toBe("number");
  });

  it("onFallback fires when switching providers", async () => {
    const onFallback = vi.fn();
    const err = new LLMError("down");
    err.statusCode = 502;

    register("a", () =>
      mockProvider({ complete: vi.fn().mockRejectedValue(err) })
    );
    register("b", () =>
      mockCompleteProvider({ content: "ok", role: "assistant" })
    );

    const client = createLLM("a://m", {
      fallback: ["b://m2"],
      onFallback,
    });
    await client.complete(req());
    expect(onFallback).toHaveBeenCalledWith(0, 1, err);
  });
});

// ---------------------------------------------------------------------------
// Error types + isFallbackable
// ---------------------------------------------------------------------------

describe("error types", () => {
  it("LLMError is instanceof Error", () => {
    const e = new LLMError("test");
    expect(e).toBeInstanceOf(Error);
    expect(e).toBeInstanceOf(LLMError);
    expect(e.message).toBe("test");
  });

  it("ProviderNotFoundError has scheme", () => {
    const e = new ProviderNotFoundError("openai");
    expect(e.scheme).toBe("openai");
    expect(e).toBeInstanceOf(LLMError);
  });

  it("CapabilityNotSupportedError has capability + provider", () => {
    const e = new CapabilityNotSupportedError("stream", "ollama");
    expect(e.capability).toBe("stream");
    expect(e.provider).toBe("ollama");
  });

  it("AuthError has provider", () => {
    const e = new AuthError("openai");
    expect(e.provider).toBe("openai");
  });

  it("RateLimitError has provider + optional retryAfter", () => {
    const e = new RateLimitError("openai", 30);
    expect(e.provider).toBe("openai");
    expect(e.retryAfter).toBe(30);
  });

  it("ModelError has model + provider", () => {
    const e = new ModelError("gpt-5", "openai");
    expect(e.model).toBe("gpt-5");
    expect(e.provider).toBe("openai");
  });

  it("FallbackExhaustedError has errors array", () => {
    const errs = [new Error("a"), new Error("b")];
    const e = new FallbackExhaustedError(errs);
    expect(e.errors).toHaveLength(2);
  });
});

describe("isFallbackable", () => {
  it("returns true for 5xx LLMError", () => {
    const e = new LLMError("server error");
    e.statusCode = 500;
    expect(isFallbackable(e)).toBe(true);
  });

  it("returns true for 502", () => {
    const e = new LLMError("bad gateway");
    e.statusCode = 502;
    expect(isFallbackable(e)).toBe(true);
  });

  it("returns false for 4xx", () => {
    const e = new LLMError("not found");
    e.statusCode = 404;
    expect(isFallbackable(e)).toBe(false);
  });

  it("returns false for plain Error", () => {
    expect(isFallbackable(new Error("plain"))).toBe(false);
  });

  it("returns true for RateLimitError (429)", () => {
    const e = new RateLimitError("openai");
    expect(isFallbackable(e)).toBe(true);
  });

  it("returns true for connection/timeout errors (LLMError)", () => {
    const e = new LLMError("ECONNREFUSED");
    e.code = "ECONNREFUSED";
    expect(isFallbackable(e)).toBe(true);
  });

  it("returns true for NodeJS.ErrnoException with ECONNREFUSED", () => {
    const e = new Error("connect ECONNREFUSED") as Error & { code: string };
    e.code = "ECONNREFUSED";
    expect(isFallbackable(e)).toBe(true);
  });

  it("returns true for NodeJS.ErrnoException with ETIMEDOUT", () => {
    const e = new Error("connect ETIMEDOUT") as Error & { code: string };
    e.code = "ETIMEDOUT";
    expect(isFallbackable(e)).toBe(true);
  });
});
