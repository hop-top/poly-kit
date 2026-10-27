/**
 * Tests for RouteLLM adapter (routellm.ts).
 * Covers: parseModelField, parseRouterConfig, scheme registration.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { resolve } from "./llm";
import { RouteLLMAdapter, parseModelField, parseRouterConfig } from "./routellm";

// ─── parseModelField ──────────────────────────────────────────────────────────

describe("parseModelField", () => {
  // Router pair: the text after the last colon parses as a finite float.
  const pairs: [string, string, string, number][] = [
    ["valid", "mf:0.7", "mf", 0.7],
    ["zero", "bert:0", "bert", 0],
    ["one", "causal_llm:1", "causal_llm", 1],
    ["precise", "mf:0.123456", "mf", 0.123456],
    ["exponent", "mf:5e-1", "mf", 0.5],
    ["leading dot", "mf:.5", "mf", 0.5],
    ["trailing dot", "mf:1.", "mf", 1],
    ["hex float", "mf:0x1p-1", "mf", 0.5],
    ["colon in router name", "a:b:0.5", "a:b", 0.5],
    ["out of range still a pair", "mf:1.5", "mf", 1.5],
    // Go ParseFloat syntax, probed against strconv.
    ["upper-case hex float", "mf:0X.8P1", "mf", 1],
    ["underscore after hex prefix", "mf:0x_1p-1", "mf", 0.5],
    ["underscore between digits", "mf:1_0", "mf", 10],
    ["underscore in exponent", "mf:1e+0_1", "mf", 10],
    ["leading zeros", "mf:00.5", "mf", 0.5],
    ["underflow to zero", "mf:1e-400", "mf", 0],
  ];
  for (const [name, model, router, threshold] of pairs) {
    it(`router pair: ${name}`, () => {
      const r = parseModelField(model);
      expect(r.isTier).toBe(false);
      expect(r.tier).toBe("");
      expect(r.routerName).toBe(router);
      expect(r.threshold).toBe(threshold);
    });
  }

  // Tier: everything else non-empty, verbatim.
  const tiers: [string, string][] = [
    ["bare tier", "private"],
    ["underscore tier", "coding_fast"],
    ["server router id", "router-mf-0.5"],
    ["non-numeric suffix", "mf:abc"],
    ["colon in tier name", "a:b"],
    ["empty name", ":0.5"],
    ["empty threshold", "mf:"],
    ["NaN suffix", "mf:NaN"],
    ["Inf suffix", "mf:Inf"],
    ["Infinity suffix", "mf:Infinity"],
    ["overflowing suffix", "mf:1e400"],
    ["whitespace suffix", "mf: 0.5"],
    ["hex integer suffix", "mf:0x10"],
    ["empty-after-sign suffix", "mf:+"],
    ["double underscore", "mf:1__0"],
    ["leading underscore", "mf:_1"],
    ["trailing underscore", "mf:1_"],
    ["underscore before dot", "mf:1_.5"],
    ["underscore before hex exponent", "mf:0x1_p0"],
    ["underscore inside hex prefix", "mf:0_x1p0"],
  ];
  for (const [name, model] of tiers) {
    it(`tier: ${name}`, () => {
      const r = parseModelField(model);
      expect(r.isTier).toBe(true);
      expect(r.tier).toBe(model);
      expect(r.routerName).toBe("");
      expect(r.threshold).toBe(0);
    });
  }

  it("throws on empty string", () => {
    expect(() => parseModelField("")).toThrow(
      "model field is required (router_name:threshold or tier name)"
    );
  });
});

// ─── Adapter: model field on the wire ─────────────────────────────────────────

describe("RouteLLMAdapter model field", () => {
  const origFetch = globalThis.fetch;
  let sent: string[];

  beforeEach(() => {
    sent = [];
    globalThis.fetch = vi.fn(async (_url: unknown, init?: RequestInit) => {
      sent.push((JSON.parse(String(init?.body)) as { model: string }).model);
      return new globalThis.Response(
        JSON.stringify({
          choices: [
            { message: { role: "assistant", content: "ok" }, finish_reason: "stop" },
          ],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } }
      );
    }) as unknown as typeof globalThis.fetch;
  });

  afterEach(() => {
    globalThis.fetch = origFetch;
  });

  function adapter(model: string, uriOnly = false): RouteLLMAdapter {
    return new RouteLLMAdapter({
      uri: { scheme: "routellm", model },
      provider: {
        model: uriOnly ? "" : model,
        apiKey: "test-key",
        baseURL: "http://127.0.0.1:6060",
      },
      fallbacks: [],
      hermetic: true,
    });
  }

  const wire: [string, string, string][] = [
    // Router pair: translated to the server's router id.
    ["router pair", "mf:0.7", "router-mf-0.7"],
    ["threshold zero", "bert:0", "router-bert-0"],
    ["threshold one", "causal_llm:1", "router-causal_llm-1"],
    ["trailing zeros trimmed", "mf:0.50", "router-mf-0.5"],
    ["exponent expanded", "mf:5e-1", "router-mf-0.5"],
    ["tiny threshold without exponent", "mf:1e-7", "router-mf-0.0000001"],
    ["negative zero", "mf:-0", "router-mf--0"],
    // Tier: verbatim, server resolves router and threshold.
    ["tier", "private", "private"],
    ["tier underscore", "coding_fast", "coding_fast"],
    ["tier server router id", "router-mf-0.5", "router-mf-0.5"],
    ["tier with colon", "a:b", "a:b"],
  ];
  for (const [name, model, want] of wire) {
    it(`sends ${name} as ${want}`, async () => {
      const a = adapter(model);
      await a.complete({ messages: [{ role: "user", content: "hi" }] });
      expect(sent).toEqual([want]);
      a.close();
    });
  }

  it("exposes a tier", () => {
    const a = adapter("private");
    expect(a.isTier()).toBe(true);
    expect(a.tier).toBe("private");
    expect(a.routerName).toBe("");
    expect(a.threshold).toBe(0);
    a.close();
  });

  it("exposes a router pair", () => {
    const a = adapter("mf:0");
    expect(a.isTier()).toBe(false);
    expect(a.tier).toBe("");
    expect(a.routerName).toBe("mf");
    expect(a.threshold).toBe(0);
    a.close();
  });

  it("falls back to the URI model", async () => {
    const a = adapter("bert:0.5", true);
    await a.complete({ messages: [{ role: "user", content: "hi" }] });
    expect(sent).toEqual(["router-bert-0.5"]);
    a.close();
  });

  it("rejects an empty model", () => {
    expect(() => adapter("")).toThrow(
      "routellm: model field is required (router_name:threshold or tier name)"
    );
  });

  it("rejects a threshold below range", () => {
    expect(() => adapter("mf:-0.1")).toThrow(
      "routellm: threshold -0.1000 out of range [0, 1]"
    );
  });

  it("rejects a threshold above range", () => {
    expect(() => adapter("mf:1.5")).toThrow(
      "routellm: threshold 1.5000 out of range [0, 1]"
    );
  });

  it("resolves a tier URI", () => {
    const p = resolve("routellm://private");
    expect(p).toBeDefined();
    p.close();
  });
});

// ─── parseRouterConfig ────────────────────────────────────────────────────────

describe("parseRouterConfig", () => {
  // Save and restore env vars around each test.
  const envKeys = [
    "ROUTELLM_BASE_URL",
    "ROUTELLM_STRONG_MODEL",
    "ROUTELLM_WEAK_MODEL",
    "ROUTELLM_ROUTERS",
  ] as const;

  const saved: Record<string, string | undefined> = {};

  beforeEach(() => {
    for (const k of envKeys) {
      saved[k] = process.env[k];
      delete process.env[k];
    }
  });

  afterEach(() => {
    for (const k of envKeys) {
      if (saved[k] !== undefined) {
        process.env[k] = saved[k];
      } else {
        delete process.env[k];
      }
    }
  });

  it("returns defaults when extras is undefined", () => {
    const cfg = parseRouterConfig(undefined);
    expect(cfg.baseUrl).toBe("http://localhost:6060");
    expect(cfg.grpcPort).toBe(6061);
    expect(cfg.strongModel).toBe("");
    expect(cfg.weakModel).toBe("");
    expect(cfg.routers).toEqual([]);
    expect(cfg.autostart).toBe(false);
    expect(cfg.eva).toEqual({ contracts: [], enforce: false });
  });

  it("returns defaults when extras has no routellm key", () => {
    const cfg = parseRouterConfig({ other: "stuff" });
    expect(cfg.baseUrl).toBe("http://localhost:6060");
  });

  it("extracts config from extras routellm sub-map", () => {
    const cfg = parseRouterConfig({
      routellm: {
        base_url: "http://custom:9090",
        grpc_port: 7070,
        strong_model: "gpt-4",
        weak_model: "gpt-3.5-turbo",
        routers: ["mf", "bert"],
        autostart: true,
        eva: { contracts: ["c1"], enforce: true },
      },
    });

    expect(cfg.baseUrl).toBe("http://custom:9090");
    expect(cfg.grpcPort).toBe(7070);
    expect(cfg.strongModel).toBe("gpt-4");
    expect(cfg.weakModel).toBe("gpt-3.5-turbo");
    expect(cfg.routers).toEqual(["mf", "bert"]);
    expect(cfg.autostart).toBe(true);
    expect(cfg.eva.contracts).toEqual(["c1"]);
    expect(cfg.eva.enforce).toBe(true);
  });

  it("env vars override extras", () => {
    process.env["ROUTELLM_BASE_URL"] = "http://env:1111";
    process.env["ROUTELLM_STRONG_MODEL"] = "env-strong";
    process.env["ROUTELLM_WEAK_MODEL"] = "env-weak";
    process.env["ROUTELLM_ROUTERS"] = "r1, r2";

    const cfg = parseRouterConfig({
      routellm: {
        base_url: "http://config:9090",
        strong_model: "config-strong",
      },
    });

    expect(cfg.baseUrl).toBe("http://env:1111");
    expect(cfg.strongModel).toBe("env-strong");
    expect(cfg.weakModel).toBe("env-weak");
    expect(cfg.routers).toEqual(["r1", "r2"]);
  });

  it("env vars override defaults when no extras", () => {
    process.env["ROUTELLM_BASE_URL"] = "http://env-only:2222";

    const cfg = parseRouterConfig(undefined);
    expect(cfg.baseUrl).toBe("http://env-only:2222");
  });

  it("throws when routellm key is not an object", () => {
    expect(() => parseRouterConfig({ routellm: "bad" })).toThrow(
      "expected map"
    );
  });
});

// ─── Scheme registration ──────────────────────────────────────────────────────

describe("routellm scheme registration", () => {
  it("side-effect import registers the routellm scheme", () => {
    // The top-level import of ./routellm triggers register("routellm", ...).
    // Verify that resolve("routellm://mf:0.5") does NOT throw
    // ProviderNotFoundError — it returns a Provider.
    const provider = resolve("routellm://mf:0.5");
    expect(provider).toBeDefined();
    expect(typeof provider.close).toBe("function");
    provider.close();
  });

  it("exports RouteLLMAdapter and helpers", async () => {
    const mod = await import("./routellm");
    expect(mod.RouteLLMAdapter).toBeDefined();
    expect(mod.parseModelField).toBeDefined();
    expect(mod.parseRouterConfig).toBeDefined();
  });
});
