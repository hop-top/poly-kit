/**
 * Integration tests for the router package.
 *
 * Tests the full request flow: Controller -> Router -> model selection.
 * Uses in-memory routers and mocks — no external dependencies.
 */

import { describe, it, expect } from "vitest";
import { Controller, parseModelName } from "./controller";
import { RandomRouter } from "./random";
import { RoutingError } from "./router";
import type { Router, Middleware, ModelPair } from "./router";

// ─── Test helpers ────────────────────────────────────────────────────────────

/** Returns a fixed score (simulates Triton MF/BERT scorer). */
class FixedRouter implements Router {
  constructor(private readonly _score: number) {}
  async score(_prompt: string): Promise<number> {
    return this._score;
  }
}

/** Always throws an error (simulates scorer failure). */
class FailingRouter implements Router {
  constructor(private readonly _err: Error) {}
  async score(_prompt: string): Promise<number> {
    throw this._err;
  }
}

/** Overrides model pair when prompt contains trigger keyword. */
class IntentMiddleware implements Middleware {
  constructor(
    private readonly trigger: string,
    private readonly pair: ModelPair,
  ) {}

  async getModelPair(prompt: string): Promise<ModelPair | null> {
    if (prompt.includes(this.trigger)) {
      return this.pair;
    }
    return null;
  }
}

// ─── Full request flow ──────────────────────────────────────────────────────

describe("integration: full request flow", () => {
  it("routes to strong model when score >= threshold", async () => {
    const ctrl = new Controller({
      strongModel: "gpt-4o",
      weakModel: "gpt-4o-mini",
      routers: { fixed: new FixedRouter(0.8) },
    });

    const model = await ctrl.route("test prompt", "fixed", 0.5);
    expect(model).toBe("gpt-4o");
  });

  it("routes to weak model when score < threshold", async () => {
    const ctrl = new Controller({
      strongModel: "gpt-4o",
      weakModel: "gpt-4o-mini",
      routers: { fixed: new FixedRouter(0.3) },
    });

    const model = await ctrl.route("test prompt", "fixed", 0.5);
    expect(model).toBe("gpt-4o-mini");
  });
});

// ─── RandomRouter (no external deps) ────────────────────────────────────────

describe("integration: RandomRouter", () => {
  it("distributes roughly evenly at threshold 0.5", async () => {
    const ctrl = new Controller({
      strongModel: "strong",
      weakModel: "weak",
      routers: { random: new RandomRouter() },
    });

    let strongCount = 0;
    let weakCount = 0;
    const n = 200;

    for (let i = 0; i < n; i++) {
      const model = await ctrl.route(`prompt ${i}`, "random", 0.5);
      if (model === "strong") strongCount++;
      else weakCount++;
    }

    // With random scores, expect roughly even split.
    expect(strongCount).toBeGreaterThan(20);
    expect(weakCount).toBeGreaterThan(20);
    expect(strongCount + weakCount).toBe(n);
  });
});

// ─── Mock Triton scorer (MF/BERT) ──────────────────────────────────────────

describe("integration: mock Triton scorer", () => {
  it("high score routes to strong model", async () => {
    const ctrl = new Controller({
      strongModel: "gpt-4o",
      weakModel: "gpt-4o-mini",
      routers: { mf: new FixedRouter(0.9) },
    });

    const model = await ctrl.route("complex math problem", "mf", 0.7);
    expect(model).toBe("gpt-4o");
  });

  it("low score routes to weak model", async () => {
    const ctrl = new Controller({
      strongModel: "gpt-4o",
      weakModel: "gpt-4o-mini",
      routers: { mf: new FixedRouter(0.3) },
    });

    const model = await ctrl.route("hi", "mf", 0.7);
    expect(model).toBe("gpt-4o-mini");
  });

  it("BERT scorer behaves same as MF", async () => {
    const ctrl = new Controller({
      strongModel: "claude-opus",
      weakModel: "claude-haiku",
      routers: { bert: new FixedRouter(0.6) },
    });

    // Score 0.6 >= threshold 0.5 -> strong.
    expect(await ctrl.route("test", "bert", 0.5)).toBe("claude-opus");
    // Score 0.6 < threshold 0.7 -> weak.
    expect(await ctrl.route("test", "bert", 0.7)).toBe("claude-haiku");
  });
});

// ─── Intent middleware routing ──────────────────────────────────────────────

describe("integration: intent middleware", () => {
  it("overrides model pair when trigger keyword present", async () => {
    const mw = new IntentMiddleware("code review", {
      strong: "claude-opus",
      weak: "claude-haiku",
    });

    const ctrl = new Controller({
      strongModel: "gpt-4o",
      weakModel: "gpt-4o-mini",
      routers: { fixed: new FixedRouter(0.8) },
      middleware: [mw],
    });

    // Without trigger: default pair, score 0.8 >= 0.5 -> strong.
    const m1 = await ctrl.route("hello world", "fixed", 0.5);
    expect(m1).toBe("gpt-4o");

    // With trigger: overridden pair, score 0.8 >= 0.5 -> strong.
    const m2 = await ctrl.route("do a code review", "fixed", 0.5);
    expect(m2).toBe("claude-opus");
  });

  it("uses default pair when middleware returns null", async () => {
    const mw = new IntentMiddleware("never-match-this", {
      strong: "x",
      weak: "y",
    });

    const ctrl = new Controller({
      strongModel: "default-strong",
      weakModel: "default-weak",
      routers: { fixed: new FixedRouter(0.8) },
      middleware: [mw],
    });

    const model = await ctrl.route("test", "fixed", 0.5);
    expect(model).toBe("default-strong");
  });
});

// ─── Threshold calibration ──────────────────────────────────────────────────

describe("integration: threshold calibration", () => {
  const score = 0.6;

  it("threshold below score -> strong", async () => {
    const ctrl = new Controller({
      strongModel: "strong",
      weakModel: "weak",
      routers: { cal: new FixedRouter(score) },
    });
    expect(await ctrl.route("test", "cal", 0.5)).toBe("strong");
  });

  it("threshold at score -> strong (>=)", async () => {
    const ctrl = new Controller({
      strongModel: "strong",
      weakModel: "weak",
      routers: { cal: new FixedRouter(score) },
    });
    expect(await ctrl.route("test", "cal", 0.6)).toBe("strong");
  });

  it("threshold above score -> weak", async () => {
    const ctrl = new Controller({
      strongModel: "strong",
      weakModel: "weak",
      routers: { cal: new FixedRouter(score) },
    });
    expect(await ctrl.route("test", "cal", 0.7)).toBe("weak");
  });
});

// ─── Error cases ────────────────────────────────────────────────────────────

describe("integration: error cases", () => {
  it("throws on invalid model string (no prefix)", () => {
    expect(() => parseModelName("mf-0.5")).toThrow(RoutingError);
  });

  it("throws on invalid model string (missing threshold)", () => {
    expect(() => parseModelName("router-mf")).toThrow(RoutingError);
  });

  it("throws on invalid model string (NaN threshold)", () => {
    expect(() => parseModelName("router-mf-abc")).toThrow(RoutingError);
  });

  it("throws on unknown router", async () => {
    const ctrl = new Controller({
      strongModel: "s",
      weakModel: "w",
      routers: { known: new FixedRouter(0.5) },
    });

    await expect(
      ctrl.route("test", "unknown", 0.5),
    ).rejects.toThrow(RoutingError);
  });

  it("throws on threshold out of [0,1]", async () => {
    const ctrl = new Controller({
      strongModel: "s",
      weakModel: "w",
      routers: { test: new FixedRouter(0.5) },
    });

    await expect(ctrl.route("test", "test", 1.5)).rejects.toThrow(
      RoutingError,
    );
    await expect(ctrl.route("test", "test", -0.1)).rejects.toThrow(
      RoutingError,
    );
  });

  it("propagates scorer errors", async () => {
    const ctrl = new Controller({
      strongModel: "s",
      weakModel: "w",
      routers: {
        broken: new FailingRouter(new Error("triton down")),
      },
    });

    await expect(
      ctrl.route("test", "broken", 0.5),
    ).rejects.toThrow("triton down");
  });
});

// ─── Model counts tracking ─────────────────────────────────────────────────

describe("integration: model counts tracking", () => {
  it("tracks routing decisions per router per model", async () => {
    const ctrl = new Controller({
      strongModel: "strong",
      weakModel: "weak",
      routers: {
        high: new FixedRouter(0.9),
        low: new FixedRouter(0.1),
      },
    });

    await ctrl.route("test", "high", 0.5);
    await ctrl.route("test", "low", 0.5);
    await ctrl.route("test", "high", 0.5);

    expect(ctrl.modelCounts.get("high")?.get("strong")).toBe(2);
    expect(ctrl.modelCounts.get("low")?.get("weak")).toBe(1);
  });
});

// ─── routeByModelName ───────────────────────────────────────────────────────

describe("integration: routeByModelName", () => {
  it("parses model name and routes correctly", async () => {
    const ctrl = new Controller({
      strongModel: "gpt-4o",
      weakModel: "gpt-4o-mini",
      routers: { mf: new FixedRouter(0.8) },
    });

    const model = await ctrl.routeByModelName(
      "test", "router-mf-0.5",
    );
    expect(model).toBe("gpt-4o");
  });
});
