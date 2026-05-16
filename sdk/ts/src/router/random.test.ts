import { describe, it, expect } from "vitest";
import { RandomRouter } from "./random";

describe("RandomRouter", () => {
  it("returns a number in [0, 1]", async () => {
    const router = new RandomRouter();
    for (let i = 0; i < 50; i++) {
      const score = await router.score("anything");
      expect(score).toBeGreaterThanOrEqual(0);
      expect(score).toBeLessThanOrEqual(1);
    }
  });

  it("ignores prompt content", async () => {
    const router = new RandomRouter();
    const a = await router.score("hello");
    const b = await router.score("world");
    // Both should be valid numbers (can't test inequality
    // deterministically, but they should be finite).
    expect(Number.isFinite(a)).toBe(true);
    expect(Number.isFinite(b)).toBe(true);
  });
});
