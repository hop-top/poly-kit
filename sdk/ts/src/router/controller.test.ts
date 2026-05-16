import { describe, it, expect, vi } from "vitest";
import { Controller, parseModelName } from "./controller";
import { RoutingError } from "./router";
import type { Router, Middleware, ModelPair } from "./router";

// ─── Helpers ─────────────────────────────────────────────────────────────────

function fixedRouter(value: number): Router {
  return { score: vi.fn(async () => value) };
}

function overrideMiddleware(pair: ModelPair): Middleware {
  return { getModelPair: vi.fn(async () => pair) };
}

function nullMiddleware(): Middleware {
  return { getModelPair: vi.fn(async () => null) };
}

// ─── parseModelName ──────────────────────────────────────────────────────────

describe("parseModelName", () => {
  it("parses router-mf-0.7", () => {
    const r = parseModelName("router-mf-0.7");
    expect(r.routerName).toBe("mf");
    expect(r.threshold).toBe(0.7);
  });

  it("parses router-sw_ranking-0.5", () => {
    const r = parseModelName("router-sw_ranking-0.5");
    expect(r.routerName).toBe("sw_ranking");
    expect(r.threshold).toBe(0.5);
  });

  it("parses threshold 0 and 1", () => {
    expect(parseModelName("router-bert-0").threshold).toBe(0);
    expect(parseModelName("router-bert-1").threshold).toBe(1);
  });

  it("throws when prefix is missing", () => {
    expect(() => parseModelName("mf-0.5")).toThrow(RoutingError);
  });

  it("throws when threshold is missing", () => {
    expect(() => parseModelName("router-mf")).toThrow(RoutingError);
  });

  it("throws on non-numeric threshold", () => {
    expect(() => parseModelName("router-mf-abc")).toThrow(RoutingError);
  });
});

// ─── Controller ──────────────────────────────────────────────────────────────

describe("Controller", () => {
  it("routes to strong model when score >= threshold", async () => {
    const ctrl = new Controller({
      strongModel: "gpt-4",
      weakModel: "gpt-3.5",
      routers: { mf: fixedRouter(0.8) },
    });

    const model = await ctrl.route("hello", "mf", 0.5);
    expect(model).toBe("gpt-4");
  });

  it("routes to weak model when score < threshold", async () => {
    const ctrl = new Controller({
      strongModel: "gpt-4",
      weakModel: "gpt-3.5",
      routers: { mf: fixedRouter(0.3) },
    });

    const model = await ctrl.route("hello", "mf", 0.5);
    expect(model).toBe("gpt-3.5");
  });

  it("throws on unknown router", async () => {
    const ctrl = new Controller({
      strongModel: "gpt-4",
      weakModel: "gpt-3.5",
    });

    await expect(ctrl.route("hi", "missing", 0.5)).rejects.toThrow(
      RoutingError,
    );
  });

  it("throws on invalid threshold", async () => {
    const ctrl = new Controller({
      strongModel: "gpt-4",
      weakModel: "gpt-3.5",
      routers: { mf: fixedRouter(0.5) },
    });

    await expect(ctrl.route("hi", "mf", 1.5)).rejects.toThrow(
      RoutingError,
    );
    await expect(ctrl.route("hi", "mf", -0.1)).rejects.toThrow(
      RoutingError,
    );
  });

  it("tracks model counts", async () => {
    const ctrl = new Controller({
      strongModel: "gpt-4",
      weakModel: "gpt-3.5",
      routers: { mf: fixedRouter(0.8) },
    });

    await ctrl.route("a", "mf", 0.5);
    await ctrl.route("b", "mf", 0.5);
    const counts = ctrl.modelCounts.get("mf")!;
    expect(counts.get("gpt-4")).toBe(2);
  });

  it("addRouter registers a new router", async () => {
    const ctrl = new Controller({
      strongModel: "gpt-4",
      weakModel: "gpt-3.5",
    });
    ctrl.addRouter("test", fixedRouter(1));
    expect(ctrl.routerNames()).toContain("test");
    const m = await ctrl.route("hi", "test", 0.5);
    expect(m).toBe("gpt-4");
  });

  it("routeByModelName delegates correctly", async () => {
    const ctrl = new Controller({
      strongModel: "gpt-4",
      weakModel: "gpt-3.5",
      routers: { bert: fixedRouter(0.9) },
    });

    const m = await ctrl.routeByModelName("hi", "router-bert-0.5");
    expect(m).toBe("gpt-4");
  });

  it("middleware can override model pair", async () => {
    const custom: ModelPair = {
      strong: "claude-3",
      weak: "mixtral",
    };
    const ctrl = new Controller({
      strongModel: "gpt-4",
      weakModel: "gpt-3.5",
      routers: { mf: fixedRouter(0.9) },
      middleware: [overrideMiddleware(custom)],
    });

    const m = await ctrl.route("hi", "mf", 0.5);
    expect(m).toBe("claude-3");
  });

  it("null-returning middleware keeps default pair", async () => {
    const ctrl = new Controller({
      strongModel: "gpt-4",
      weakModel: "gpt-3.5",
      routers: { mf: fixedRouter(0.9) },
      middleware: [nullMiddleware()],
    });

    const m = await ctrl.route("hi", "mf", 0.5);
    expect(m).toBe("gpt-4");
  });

  it("last middleware wins", async () => {
    const first: ModelPair = { strong: "a", weak: "b" };
    const second: ModelPair = { strong: "x", weak: "y" };
    const ctrl = new Controller({
      strongModel: "gpt-4",
      weakModel: "gpt-3.5",
      routers: { mf: fixedRouter(0.1) },
      middleware: [
        overrideMiddleware(first),
        overrideMiddleware(second),
      ],
    });

    const m = await ctrl.route("hi", "mf", 0.5);
    expect(m).toBe("y"); // weak, score=0.1 < 0.5
  });
});
