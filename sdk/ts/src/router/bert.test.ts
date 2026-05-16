import { describe, it, expect, vi } from "vitest";
import { BERTRouter, softmax } from "./bert";
import type { Tokenizer, TritonInfer } from "./bert";
import type { InferRequest, InferResponse } from "../triton/client";

// ─── softmax ─────────────────────────────────────────────────────────────────

describe("softmax", () => {
  it("sums to 1", () => {
    const probs = softmax([1, 2, 3]);
    const sum = probs.reduce((a, b) => a + b, 0);
    expect(sum).toBeCloseTo(1);
  });

  it("highest logit gets highest probability", () => {
    const probs = softmax([1, 5, 2]);
    expect(probs[1]).toBeGreaterThan(probs[0]);
    expect(probs[1]).toBeGreaterThan(probs[2]);
  });

  it("equal logits give uniform distribution", () => {
    const probs = softmax([3, 3, 3]);
    expect(probs[0]).toBeCloseTo(1 / 3);
    expect(probs[1]).toBeCloseTo(1 / 3);
    expect(probs[2]).toBeCloseTo(1 / 3);
  });

  it("handles large logits without overflow", () => {
    const probs = softmax([1000, 1001, 1002]);
    const sum = probs.reduce((a, b) => a + b, 0);
    expect(sum).toBeCloseTo(1);
  });

  it("handles negative logits", () => {
    const probs = softmax([-5, -3, -1]);
    const sum = probs.reduce((a, b) => a + b, 0);
    expect(sum).toBeCloseTo(1);
    expect(probs[2]).toBeGreaterThan(probs[0]);
  });
});

// ─── BERTRouter ──────────────────────────────────────────────────────────────

function stubTokenizer(): Tokenizer {
  return {
    encode: vi.fn((_text: string) => ({
      inputIds: [101, 2023, 2003, 1037, 3231, 102],
      attentionMask: [1, 1, 1, 1, 1, 1],
    })),
  };
}

function mockTritonInfer(logits: number[]): TritonInfer {
  return {
    infer: vi.fn(async (_req: InferRequest): Promise<InferResponse> => ({
      model_name: "bert",
      outputs: [
        {
          name: "logits",
          shape: [1, logits.length],
          datatype: "FP32",
          data: logits,
        },
      ],
    })),
  };
}

describe("BERTRouter", () => {
  it("high strong-win logit yields high score", async () => {
    // logits: [strong_wins=10, tie=0, weak_wins=0]
    const triton = mockTritonInfer([10, 0, 0]);
    const router = new BERTRouter({
      triton,
      tokenizer: stubTokenizer(),
    });

    const score = await router.score("test prompt");
    // softmax([10,0,0]) ~ [0.9999, ~0, ~0]
    // binaryProb = ~0 + ~0 ~ 0, score = 1 - ~0 ~ 1
    expect(score).toBeGreaterThan(0.95);
  });

  it("high weak-win logit yields low score", async () => {
    // logits: [strong_wins=0, tie=0, weak_wins=10]
    const triton = mockTritonInfer([0, 0, 10]);
    const router = new BERTRouter({
      triton,
      tokenizer: stubTokenizer(),
    });

    const score = await router.score("test prompt");
    // binaryProb = softmax[1] + softmax[2] ~ 0 + ~1 ~ 1
    // score = 1 - 1 ~ 0
    expect(score).toBeLessThan(0.05);
  });

  it("equal logits yield moderate score", async () => {
    const triton = mockTritonInfer([1, 1, 1]);
    const router = new BERTRouter({
      triton,
      tokenizer: stubTokenizer(),
    });

    const score = await router.score("test");
    // binaryProb = 1/3 + 1/3 = 2/3, score = 1/3
    expect(score).toBeCloseTo(1 / 3, 2);
  });

  it("sends correct input tensor names", async () => {
    const triton = mockTritonInfer([1, 1, 1]);
    const router = new BERTRouter({
      triton,
      tokenizer: stubTokenizer(),
    });

    await router.score("test");
    const call = (triton.infer as ReturnType<typeof vi.fn>).mock.calls[0];
    const req = call[0] as InferRequest;
    expect(req.inputs[0].name).toBe("input_ids");
    expect(req.inputs[1].name).toBe("attention_mask");
    expect(req.outputs![0].name).toBe("logits");
  });

  it("uses custom tensor names", async () => {
    const triton: TritonInfer = {
      infer: vi.fn(async () => ({
        model_name: "bert",
        outputs: [
          {
            name: "custom_out",
            shape: [1, 3],
            datatype: "FP32" as const,
            data: [1, 1, 1],
          },
        ],
      })),
    };

    const router = new BERTRouter({
      triton,
      tokenizer: stubTokenizer(),
      inputIdsName: "ids",
      attentionMaskName: "mask",
      outputName: "custom_out",
    });

    await router.score("test");
    const req = (triton.infer as ReturnType<typeof vi.fn>).mock
      .calls[0][0] as InferRequest;
    expect(req.inputs[0].name).toBe("ids");
    expect(req.inputs[1].name).toBe("mask");
  });

  it("returns 1 when output is empty (defaults to strong)", async () => {
    const triton: TritonInfer = {
      infer: vi.fn(async () => ({
        model_name: "bert",
        outputs: [],
      })),
    };

    const router = new BERTRouter({
      triton,
      tokenizer: stubTokenizer(),
    });

    const score = await router.score("test");
    expect(score).toBe(1);
  });

  it("tokenizer receives the prompt text", async () => {
    const tokenizer = stubTokenizer();
    const triton = mockTritonInfer([1, 1, 1]);
    const router = new BERTRouter({ triton, tokenizer });

    await router.score("my special prompt");
    expect(tokenizer.encode).toHaveBeenCalledWith("my special prompt");
  });
});
