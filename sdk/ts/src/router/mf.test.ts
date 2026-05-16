import { describe, it, expect, vi } from "vitest";
import { MFRouter } from "./mf";
import type { TritonScorer } from "../triton/client";

function mockTriton(returnValue: number): TritonScorer {
  return { score: vi.fn(async () => returnValue) };
}

function mockEmbed(dim = 4): (prompt: string) => Promise<Float32Array> {
  return vi.fn(async () => new Float32Array(dim).fill(0.5));
}

describe("MFRouter", () => {
  it("returns the win rate from Triton", async () => {
    const triton = mockTriton(0.75);
    const embed = mockEmbed();
    const router = new MFRouter({ triton, embed });

    const score = await router.score("hello world");
    expect(score).toBe(0.75);
    expect(embed).toHaveBeenCalledWith("hello world");
    expect(triton.score).toHaveBeenCalled();
  });

  it("clamps values above 1 to 1", async () => {
    const router = new MFRouter({
      triton: mockTriton(1.5),
      embed: mockEmbed(),
    });
    expect(await router.score("test")).toBe(1);
  });

  it("clamps values below 0 to 0", async () => {
    const router = new MFRouter({
      triton: mockTriton(-0.3),
      embed: mockEmbed(),
    });
    expect(await router.score("test")).toBe(0);
  });

  it("passes the embedding to Triton scorer", async () => {
    const triton = mockTriton(0.5);
    const fixedEmb = new Float32Array([1, 2, 3]);
    const embed = vi.fn(async () => fixedEmb);
    const router = new MFRouter({ triton, embed });

    await router.score("prompt");
    expect(triton.score).toHaveBeenCalledWith(fixedEmb);
  });

  it("handles zero win rate", async () => {
    const router = new MFRouter({
      triton: mockTriton(0),
      embed: mockEmbed(),
    });
    expect(await router.score("test")).toBe(0);
  });

  it("handles exact 1.0 win rate", async () => {
    const router = new MFRouter({
      triton: mockTriton(1.0),
      embed: mockEmbed(),
    });
    expect(await router.score("test")).toBe(1);
  });
});
