/**
 * BERTRouter — BERT-based sequence classification router.
 *
 * Tokenizes the prompt, sends token IDs and attention mask to a
 * Triton-hosted BERT model, applies softmax to logits, and returns
 * the strong-model win rate.
 *
 * Port of Python's BERTRouter from routellm/routers/routers.py.
 */

import type { Router } from "./router";
import type { InferRequest, InferResponse } from "../triton/client";

// ─── Tokenizer interface (stubbed) ──────────────────────────────────────────

/** Tokenizer output — mirrors HuggingFace tokenizer output shape. */
export interface TokenizerOutput {
  inputIds: number[];
  attentionMask: number[];
}

/**
 * Tokenizer interface for BERT.
 *
 * Implementations should handle padding, truncation, and special tokens
 * (CLS, SEP, etc.) internally.
 */
export interface Tokenizer {
  encode(text: string): TokenizerOutput;
}

// ─── Triton caller interface ─────────────────────────────────────────────────

/**
 * Sends raw inference requests to Triton.
 * Separated from TritonClient so tests can inject a mock.
 */
export interface TritonInfer {
  infer(request: InferRequest): Promise<InferResponse>;
}

// ─── Math helpers ────────────────────────────────────────────────────────────

/**
 * Softmax over an array of logits.
 *
 * Subtracts max for numerical stability, then normalizes.
 */
export function softmax(logits: number[]): number[] {
  const maxVal = Math.max(...logits);
  const exps = logits.map((v) => Math.exp(v - maxVal));
  const sum = exps.reduce((a, b) => a + b, 0);
  return exps.map((e) => e / sum);
}

// ─── Options ─────────────────────────────────────────────────────────────────

export interface BERTRouterOptions {
  /** Triton inference caller for the BERT model. */
  triton: TritonInfer;
  /** Tokenizer for the BERT model. */
  tokenizer: Tokenizer;
  /** Number of output labels (default: 3). */
  numLabels?: number;
  /** Name for the input_ids tensor (default: "input_ids"). */
  inputIdsName?: string;
  /** Name for the attention_mask tensor (default: "attention_mask"). */
  attentionMaskName?: string;
  /** Name for the output logits tensor (default: "logits"). */
  outputName?: string;
}

// ─── Router ──────────────────────────────────────────────────────────────────

export class BERTRouter implements Router {
  private readonly _triton: TritonInfer;
  private readonly _tokenizer: Tokenizer;
  private readonly _numLabels: number;
  private readonly _inputIdsName: string;
  private readonly _attentionMaskName: string;
  private readonly _outputName: string;

  constructor(opts: BERTRouterOptions) {
    this._triton = opts.triton;
    this._tokenizer = opts.tokenizer;
    this._numLabels = opts.numLabels ?? 3;
    this._inputIdsName = opts.inputIdsName ?? "input_ids";
    this._attentionMaskName = opts.attentionMaskName ?? "attention_mask";
    this._outputName = opts.outputName ?? "logits";
  }

  /**
   * Score a prompt using BERT classification.
   *
   * 1. Tokenize the prompt.
   * 2. Send input_ids + attention_mask to Triton.
   * 3. Apply softmax to logits.
   * 4. Compute strong-model win rate as 1 - P(tie or weak wins).
   *
   * The Python code uses the last two softmax classes as the
   * "binary probability" of tie + weak winning.
   */
  async score(prompt: string): Promise<number> {
    const { inputIds, attentionMask } = this._tokenizer.encode(prompt);

    const request: InferRequest = {
      inputs: [
        {
          name: this._inputIdsName,
          shape: [1, inputIds.length],
          datatype: "INT64",
          data: inputIds,
        },
        {
          name: this._attentionMaskName,
          shape: [1, attentionMask.length],
          datatype: "INT64",
          data: attentionMask,
        },
      ],
      outputs: [{ name: this._outputName }],
    };

    const response = await this._triton.infer(request);
    const logitsOutput = response.outputs.find(
      (o) => o.name === this._outputName,
    );

    if (!logitsOutput || !logitsOutput.data.length) {
      // Default to strong model on error.
      return 1;
    }

    const logits = logitsOutput.data.slice(0, this._numLabels) as number[];
    const probs = softmax(logits);

    // binaryProb = sum of last 2 classes (tie + weak wins).
    const binaryProb =
      probs.length >= 2
        ? probs[probs.length - 2] + probs[probs.length - 1]
        : 0;

    return 1 - binaryProb;
  }
}
