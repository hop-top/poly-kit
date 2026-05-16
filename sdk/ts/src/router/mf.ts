/**
 * MFRouter — Matrix Factorization router.
 *
 * Embeds the prompt (via an embedding function), sends the embedding
 * to a Triton-hosted MF model, and returns the win rate as the score.
 *
 * Port of Python's MatrixFactorizationRouter.
 */

import type { Router } from "./router";
import type { TritonScorer } from "../triton/client";

// ─── Types ───────────────────────────────────────────────────────────────────

/** Function that produces an embedding vector for a prompt. */
export type EmbedFn = (prompt: string) => Promise<Float32Array>;

export interface MFRouterOptions {
  /** Triton scorer for the MF model. */
  triton: TritonScorer;
  /** Function to embed a prompt into a float vector. */
  embed: EmbedFn;
}

// ─── Router ──────────────────────────────────────────────────────────────────

export class MFRouter implements Router {
  private readonly _triton: TritonScorer;
  private readonly _embed: EmbedFn;

  constructor(opts: MFRouterOptions) {
    this._triton = opts.triton;
    this._embed = opts.embed;
  }

  /**
   * Score a prompt by embedding it and sending to the Triton MF model.
   *
   * Returns the win rate of the strong model in [0, 1].
   */
  async score(prompt: string): Promise<number> {
    const embedding = await this._embed(prompt);
    const winRate = await this._triton.score(embedding);
    return clamp01(winRate);
  }
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

function clamp01(v: number): number {
  return Math.max(0, Math.min(1, v));
}
