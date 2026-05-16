import { describe, it, expect, vi } from "vitest";
import {
  dot,
  norm,
  cosineSimilarities,
  getWeightings,
  computeEloMle,
  computeTiers,
  SWRankingRouter,
} from "./sw";
import type { ArenaBattle, EmbeddingFn } from "./sw";

// ─── Math helpers ────────────────────────────────────────────────────────────

describe("dot", () => {
  it("computes dot product", () => {
    expect(dot([1, 2, 3], [4, 5, 6])).toBe(32);
  });

  it("returns 0 for zero vectors", () => {
    expect(dot([0, 0], [1, 2])).toBe(0);
  });
});

describe("norm", () => {
  it("computes L2 norm", () => {
    expect(norm([3, 4])).toBe(5);
  });

  it("returns 0 for zero vector", () => {
    expect(norm([0, 0, 0])).toBe(0);
  });
});

describe("cosineSimilarities", () => {
  it("returns 1 for identical vectors", () => {
    const sims = cosineSimilarities([[1, 0]], [1, 0]);
    expect(sims[0]).toBeCloseTo(1);
  });

  it("returns 0 for orthogonal vectors", () => {
    const sims = cosineSimilarities([[1, 0]], [0, 1]);
    expect(sims[0]).toBeCloseTo(0);
  });

  it("handles zero vector input", () => {
    const sims = cosineSimilarities([[1, 0]], [0, 0]);
    expect(sims[0]).toBe(0);
  });
});

describe("getWeightings", () => {
  it("scales similarities into weightings", () => {
    const w = getWeightings([0.5, 1.0, 0.0]);
    expect(w.length).toBe(3);
    // max similarity maps to 10 * 10^1 = 100
    expect(w[1]).toBeCloseTo(100);
    // 0 similarity maps to 10 * 10^0 = 10
    expect(w[2]).toBeCloseTo(10);
  });

  it("handles all-zero similarities", () => {
    const w = getWeightings([0, 0, 0]);
    expect(w).toEqual([10, 10, 10]);
  });
});

// ─── Elo ─────────────────────────────────────────────────────────────────────

describe("computeEloMle", () => {
  it("stronger model gets higher rating", () => {
    const battles: ArenaBattle[] = [];
    for (let i = 0; i < 100; i++) {
      battles.push({ modelA: "strong", modelB: "weak", winner: "model_a" });
    }
    const ratings = computeEloMle(battles);
    expect(ratings.get("strong")!).toBeGreaterThan(ratings.get("weak")!);
  });

  it("handles ties", () => {
    const battles: ArenaBattle[] = [];
    for (let i = 0; i < 100; i++) {
      battles.push({ modelA: "a", modelB: "b", winner: "tie" });
    }
    const ratings = computeEloMle(battles);
    // With only ties, ratings should be roughly equal.
    expect(
      Math.abs(ratings.get("a")! - ratings.get("b")!),
    ).toBeLessThan(10);
  });

  it("respects weights", () => {
    const battles: ArenaBattle[] = [
      { modelA: "a", modelB: "b", winner: "model_a" },
      { modelA: "a", modelB: "b", winner: "model_b" },
    ];
    // Heavy weight on the first battle (a wins).
    const ratings = computeEloMle(battles, [100, 1]);
    expect(ratings.get("a")!).toBeGreaterThan(ratings.get("b")!);
  });
});

describe("computeTiers", () => {
  it("assigns tiers based on rating order", () => {
    const ratings = new Map([
      ["a", 800],
      ["b", 1000],
      ["c", 1200],
      ["d", 1400],
    ]);
    const tiers = computeTiers(ratings, 2);
    expect(tiers.get("a")).toBeLessThan(tiers.get("d")!);
  });
});

// ─── SWRankingRouter ─────────────────────────────────────────────────────────

describe("SWRankingRouter", () => {
  function makeBattles(): ArenaBattle[] {
    const battles: ArenaBattle[] = [];
    for (let i = 0; i < 20; i++) {
      battles.push({
        modelA: "gpt-4-1106-preview",
        modelB: "mixtral-8x7b-instruct-v0.1",
        winner: "model_a",
      });
    }
    for (let i = 0; i < 5; i++) {
      battles.push({
        modelA: "gpt-4-1106-preview",
        modelB: "mixtral-8x7b-instruct-v0.1",
        winner: "model_b",
      });
    }
    return battles;
  }

  function makeEmbeddings(n: number, dim = 4): number[][] {
    return Array.from({ length: n }, (_, i) => {
      const v = new Array(dim).fill(0);
      v[i % dim] = 1;
      return v;
    });
  }

  it("returns a score in [0, 1]", async () => {
    const battles = makeBattles();
    const embs = makeEmbeddings(battles.length);
    const embed: EmbeddingFn = vi.fn(async () => [1, 0, 0, 0]);

    const router = new SWRankingRouter({
      battles,
      battleEmbeddings: embs,
      embed,
    });

    const score = await router.score("test prompt");
    expect(score).toBeGreaterThanOrEqual(0);
    expect(score).toBeLessThanOrEqual(1);
    expect(embed).toHaveBeenCalledWith("test prompt");
  });

  it("strong model dominance yields high score", async () => {
    // All wins for strong model.
    const battles: ArenaBattle[] = Array.from({ length: 50 }, () => ({
      modelA: "gpt-4-1106-preview",
      modelB: "mixtral-8x7b-instruct-v0.1",
      winner: "model_a" as const,
    }));
    const embs = makeEmbeddings(50);
    const embed: EmbeddingFn = async () => [1, 0, 0, 0];

    const router = new SWRankingRouter({
      battles,
      battleEmbeddings: embs,
      embed,
    });

    const score = await router.score("hi");
    expect(score).toBeGreaterThan(0.5);
  });

  it("throws on mismatched lengths", () => {
    expect(
      () =>
        new SWRankingRouter({
          battles: [
            {
              modelA: "a",
              modelB: "b",
              winner: "model_a",
            },
          ],
          battleEmbeddings: [],
          embed: async () => [0],
        }),
    ).toThrow("same length");
  });
});
