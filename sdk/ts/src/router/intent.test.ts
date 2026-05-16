import { describe, it, expect, vi } from "vitest";
import {
  IntentModelSelector,
  IntentDetector,
  cosineSimilarity,
  dot,
  norm,
} from "./intent";
import type { ModelPair } from "./router";

// ─── Math helpers ────────────────────────────────────────────────────────────

describe("dot", () => {
  it("computes dot product", () => {
    expect(dot([1, 2, 3], [4, 5, 6])).toBe(32);
  });
});

describe("norm", () => {
  it("computes L2 norm", () => {
    expect(norm([3, 4])).toBe(5);
  });
});

describe("cosineSimilarity", () => {
  it("returns 1 for identical vectors", () => {
    expect(cosineSimilarity([1, 0], [1, 0])).toBeCloseTo(1);
  });

  it("returns 0 for orthogonal vectors", () => {
    expect(cosineSimilarity([1, 0], [0, 1])).toBeCloseTo(0);
  });

  it("returns 0 for zero vector", () => {
    expect(cosineSimilarity([0, 0], [1, 0])).toBe(0);
  });

  it("returns -1 for opposite vectors", () => {
    expect(cosineSimilarity([1, 0], [-1, 0])).toBeCloseTo(-1);
  });
});

// ─── IntentModelSelector ─────────────────────────────────────────────────────

describe("IntentModelSelector", () => {
  const codePair: ModelPair = { strong: "gpt-4", weak: "gpt-3.5" };
  const writingPair: ModelPair = {
    strong: "claude-3",
    weak: "mixtral",
  };
  const defaultPair: ModelPair = {
    strong: "default-s",
    weak: "default-w",
  };

  it("returns matched model pair for detected intent", async () => {
    const detect = vi.fn(async () => "coding");
    const selector = new IntentModelSelector({
      mappings: [
        { intent: "coding", modelPair: codePair, description: "" },
        { intent: "writing", modelPair: writingPair, description: "" },
      ],
      defaultPair,
      detect,
    });

    const pair = await selector.getModelPair("write me code");
    expect(pair).toEqual(codePair);
    expect(detect).toHaveBeenCalledWith("write me code");
  });

  it("returns default pair for unknown intent", async () => {
    const detect = vi.fn(async () => "unknown");
    const selector = new IntentModelSelector({
      mappings: [
        { intent: "coding", modelPair: codePair, description: "" },
      ],
      defaultPair,
      detect,
    });

    const pair = await selector.getModelPair("random question");
    expect(pair).toEqual(defaultPair);
  });

  it("caches intent detection results", async () => {
    const detect = vi.fn(async () => "coding");
    const selector = new IntentModelSelector({
      mappings: [
        { intent: "coding", modelPair: codePair, description: "" },
      ],
      defaultPair,
      detect,
    });

    await selector.getModelPair("same prompt");
    await selector.getModelPair("same prompt");
    expect(detect).toHaveBeenCalledTimes(1);
  });

  it("clearCache resets the cache", async () => {
    const detect = vi.fn(async () => "coding");
    const selector = new IntentModelSelector({
      mappings: [
        { intent: "coding", modelPair: codePair, description: "" },
      ],
      defaultPair,
      detect,
    });

    await selector.getModelPair("prompt");
    selector.clearCache();
    await selector.getModelPair("prompt");
    expect(detect).toHaveBeenCalledTimes(2);
  });

  it("intents() returns registered intent names", () => {
    const selector = new IntentModelSelector({
      mappings: [
        { intent: "coding", modelPair: codePair, description: "" },
        { intent: "writing", modelPair: writingPair, description: "" },
      ],
      defaultPair,
      detect: async () => "coding",
    });

    expect(selector.intents()).toContain("coding");
    expect(selector.intents()).toContain("writing");
  });
});

// ─── IntentDetector ──────────────────────────────────────────────────────────

describe("IntentDetector", () => {
  // Simple embeddings: coding=[1,0,0], writing=[0,1,0]
  function mockEmbed(): (text: string) => Promise<number[]> {
    return vi.fn(async (text: string) => {
      if (text.includes("code") || text.includes("function")) {
        return [1, 0, 0];
      }
      if (text.includes("write") || text.includes("essay")) {
        return [0, 1, 0];
      }
      return [0.5, 0.5, 0];
    });
  }

  it("returns 'general' with no examples", async () => {
    const detector = new IntentDetector({ embed: mockEmbed() });
    expect(await detector.detect("anything")).toBe("general");
  });

  it("detects intent matching closest examples", async () => {
    const detector = new IntentDetector({ embed: mockEmbed() });
    await detector.addExamples("coding", ["write a function"]);
    await detector.addExamples("writing", ["write an essay"]);

    expect(await detector.detect("code a function")).toBe("coding");
    expect(await detector.detect("write my essay")).toBe("writing");
  });

  it("confidence returns normalized scores", async () => {
    const detector = new IntentDetector({ embed: mockEmbed() });
    await detector.addExamples("coding", ["code"]);
    await detector.addExamples("writing", ["essay"]);

    const conf = await detector.confidence("code something");
    let total = 0;
    for (const v of conf.values()) total += v;
    expect(total).toBeCloseTo(1);
  });

  it("confidence returns empty map with no examples", async () => {
    const detector = new IntentDetector({ embed: mockEmbed() });
    const conf = await detector.confidence("anything");
    expect(conf.size).toBe(0);
  });

  it("addExamples accumulates", async () => {
    const detector = new IntentDetector({ embed: mockEmbed() });
    await detector.addExamples("coding", ["code a"]);
    await detector.addExamples("coding", ["code b"]);

    // Should still detect coding.
    expect(await detector.detect("code something")).toBe("coding");
  });
});
