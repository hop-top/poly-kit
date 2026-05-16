import { describe, it, expect } from "vitest";
import { route, RoutingError } from "./router";
import type { ModelPair } from "./router";

const pair: ModelPair = { strong: "gpt-4", weak: "gpt-3.5" };

describe("route()", () => {
  it("returns strong when score >= threshold", () => {
    expect(route(0.7, 0.5, pair)).toBe("gpt-4");
    expect(route(0.5, 0.5, pair)).toBe("gpt-4");
    expect(route(1, 0, pair)).toBe("gpt-4");
  });

  it("returns weak when score < threshold", () => {
    expect(route(0.3, 0.5, pair)).toBe("gpt-3.5");
    expect(route(0, 0.5, pair)).toBe("gpt-3.5");
  });

  it("edge: score=0, threshold=0 returns strong", () => {
    expect(route(0, 0, pair)).toBe("gpt-4");
  });
});

describe("RoutingError", () => {
  it("has correct name and message", () => {
    const err = new RoutingError("test error");
    expect(err.name).toBe("RoutingError");
    expect(err.message).toBe("test error");
    expect(err).toBeInstanceOf(Error);
  });
});
