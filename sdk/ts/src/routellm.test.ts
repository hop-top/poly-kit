/**
 * Tests for RouteLLM adapter (routellm.ts).
 * Covers: parseModelField, parseRouterConfig, scheme registration.
 */

import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { resolve } from "./llm";
import { parseModelField, parseRouterConfig } from "./routellm";

// ─── parseModelField ──────────────────────────────────────────────────────────

describe("parseModelField", () => {
  it("parses valid router:threshold", () => {
    const r = parseModelField("mf:0.7");
    expect(r.routerName).toBe("mf");
    expect(r.threshold).toBe(0.7);
  });

  it("parses zero threshold", () => {
    const r = parseModelField("bert:0");
    expect(r.routerName).toBe("bert");
    expect(r.threshold).toBe(0);
  });

  it("parses threshold of 1", () => {
    const r = parseModelField("causal_llm:1");
    expect(r.routerName).toBe("causal_llm");
    expect(r.threshold).toBe(1);
  });

  it("parses precise threshold", () => {
    const r = parseModelField("mf:0.123456");
    expect(r.routerName).toBe("mf");
    expect(r.threshold).toBe(0.123456);
  });

  it("throws on empty string", () => {
    expect(() => parseModelField("")).toThrow("model field is required");
  });

  it("throws on missing colon", () => {
    expect(() => parseModelField("mf")).toThrow("expected format");
  });

  it("throws on empty router name", () => {
    expect(() => parseModelField(":0.5")).toThrow("expected format");
  });

  it("throws on empty threshold", () => {
    expect(() => parseModelField("mf:")).toThrow("expected format");
  });

  it("throws on non-numeric threshold", () => {
    expect(() => parseModelField("mf:abc")).toThrow("invalid threshold");
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
