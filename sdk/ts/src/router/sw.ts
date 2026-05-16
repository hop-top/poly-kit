/**
 * SWRankingRouter — Similarity-Weighted ranking router.
 *
 * Uses OpenAI embeddings to find similar arena battles, computes
 * weighted Elo ratings, and derives the strong-model win rate.
 *
 * Port of Python's SWRankingRouter from routellm/routers/routers.py.
 */

import type { Router } from "./router";

// ─── Types ───────────────────────────────────────────────────────────────────

/** A single arena battle record. */
export interface ArenaBattle {
  modelA: string;
  modelB: string;
  /** "model_a" | "model_b" | "tie" */
  winner: string;
}

/** Function that fetches an embedding vector for a prompt. */
export type EmbeddingFn = (prompt: string) => Promise<number[]>;

export interface SWRankingOptions {
  /** Pre-loaded arena battles. */
  battles: ArenaBattle[];
  /** Pre-computed embeddings for each battle (same order). */
  battleEmbeddings: number[][];
  /** Function to get prompt embedding. */
  embed: EmbeddingFn;
  /** Strong model name for Elo lookup (default: "gpt-4-1106-preview"). */
  strongModel?: string;
  /** Weak model name for Elo lookup (default: "mixtral-8x7b-instruct-v0.1"). */
  weakModel?: string;
  /** Number of Elo tiers (default: 10). */
  numTiers?: number;
}

// ─── Math helpers ────────────────────────────────────────────────────────────

/** Dot product of two vectors. Uses min length to avoid NaN on mismatch. */
export function dot(a: number[], b: number[]): number {
  const len = Math.min(a.length, b.length);
  let sum = 0;
  for (let i = 0; i < len; i++) sum += a[i] * b[i];
  return sum;
}

/** L2 norm of a vector. */
export function norm(v: number[]): number {
  let sum = 0;
  for (let i = 0; i < v.length; i++) sum += v[i] * v[i];
  return Math.sqrt(sum);
}

/**
 * Cosine similarity between each row of a matrix and a vector.
 * Returns an array of similarities.
 */
export function cosineSimilarities(
  matrix: number[][],
  vec: number[],
): number[] {
  const vecNorm = norm(vec);
  if (vecNorm === 0) return new Array(matrix.length).fill(0);

  return matrix.map((row) => {
    const rowNorm = norm(row);
    if (rowNorm === 0) return 0;
    return dot(row, vec) / (rowNorm * vecNorm);
  });
}

/**
 * Compute similarity-based weightings (10 * 10^(sim / maxSim)).
 */
export function getWeightings(similarities: number[]): number[] {
  let maxSim = -Infinity;
  for (const s of similarities) if (s > maxSim) maxSim = s;
  if (maxSim <= 0) return similarities.map(() => 10);

  return similarities.map((s) => 10 * Math.pow(10, s / maxSim));
}

// ─── Elo helpers ─────────────────────────────────────────────────────────────

/**
 * Compute Elo ratings via MLE with ties (simplified).
 *
 * This is a simplified port of compute_elo_mle_with_tie — uses
 * iterative gradient ascent on the Bradley-Terry model with ties.
 *
 * @param battles - Array of { modelA, modelB, winner }.
 * @param weights - Optional per-battle weights.
 * @param numIter - Iterations of gradient ascent.
 * @returns Map from model name to Elo rating.
 */
export function computeEloMle(
  battles: ArenaBattle[],
  weights?: number[],
  numIter = 200,
): Map<string, number> {
  // Collect unique models.
  const modelSet = new Set<string>();
  for (const b of battles) {
    modelSet.add(b.modelA);
    modelSet.add(b.modelB);
  }
  const models = [...modelSet].sort();
  const idx = new Map<string, number>();
  models.forEach((m, i) => idx.set(m, i));

  const n = models.length;
  const ratings = new Float64Array(n).fill(1000);
  const lr = 10;

  for (let iter = 0; iter < numIter; iter++) {
    const grad = new Float64Array(n);

    for (let i = 0; i < battles.length; i++) {
      const b = battles[i];
      const w = weights ? weights[i] : 1;
      const ia = idx.get(b.modelA)!;
      const ib = idx.get(b.modelB)!;

      const diff = (ratings[ib] - ratings[ia]) / 400;
      const pA = 1 / (1 + Math.pow(10, diff));

      let sA: number;
      if (b.winner === "model_a") sA = 1;
      else if (b.winner === "model_b") sA = 0;
      else sA = 0.5; // tie

      const delta = w * (sA - pA);
      grad[ia] += delta;
      grad[ib] -= delta;
    }

    for (let j = 0; j < n; j++) {
      ratings[j] += lr * grad[j];
    }
  }

  const result = new Map<string, number>();
  models.forEach((m, i) => result.set(m, ratings[i]));
  return result;
}

/**
 * Assign models to tiers based on Elo ratings.
 *
 * Models are sorted by rating and bucketed into `numTiers` tiers.
 * Returns a map from model name to tier index (0-based).
 */
export function computeTiers(
  ratings: Map<string, number>,
  numTiers: number,
): Map<string, number> {
  const sorted = [...ratings.entries()].sort((a, b) => a[1] - b[1]);
  const tierSize = Math.max(1, Math.ceil(sorted.length / numTiers));
  const result = new Map<string, number>();
  sorted.forEach(([model], i) => {
    result.set(model, Math.floor(i / tierSize));
  });
  return result;
}

// ─── Router ──────────────────────────────────────────────────────────────────

export class SWRankingRouter implements Router {
  private readonly _battles: ArenaBattle[];
  private readonly _battleEmbs: number[][];
  private readonly _embed: EmbeddingFn;
  private readonly _strongModel: string;
  private readonly _weakModel: string;
  private readonly _model2tier: Map<string, number>;
  /** Battles with models replaced by their tiers. */
  private readonly _tieredBattles: ArenaBattle[];

  constructor(opts: SWRankingOptions) {
    if (opts.battles.length !== opts.battleEmbeddings.length) {
      throw new Error(
        "battles and battleEmbeddings must have the same length",
      );
    }

    this._battles = opts.battles;
    this._battleEmbs = opts.battleEmbeddings;
    this._embed = opts.embed;
    this._strongModel = opts.strongModel ?? "gpt-4-1106-preview";
    this._weakModel = opts.weakModel ?? "mixtral-8x7b-instruct-v0.1";

    const numTiers = opts.numTiers ?? 10;

    // Compute initial Elo ratings + tiers.
    const ratings = computeEloMle(this._battles);
    this._model2tier = computeTiers(ratings, numTiers);

    // Replace model names with tier indices in battles.
    this._tieredBattles = this._battles.map((b) => ({
      modelA: String(this._model2tier.get(b.modelA) ?? 0),
      modelB: String(this._model2tier.get(b.modelB) ?? 0),
      winner: b.winner,
    }));
  }

  async score(prompt: string): Promise<number> {
    const promptEmb = await this._embed(prompt);
    const similarities = cosineSimilarities(
      this._battleEmbs,
      promptEmb,
    );
    const weightings = getWeightings(similarities);
    const wRatings = computeEloMle(this._tieredBattles, weightings);

    const strongTier = this._model2tier.get(this._strongModel);
    const weakTier = this._model2tier.get(this._weakModel);

    if (strongTier === undefined || weakTier === undefined) {
      // Unknown model in Elo data — default to strong.
      return 1;
    }

    const strongScore = wRatings.get(String(strongTier)) ?? 1000;
    const weakScore = wRatings.get(String(weakTier)) ?? 1000;

    const weakWinRate =
      1 / (1 + Math.pow(10, (strongScore - weakScore) / 400));
    return 1 - weakWinRate;
  }
}
