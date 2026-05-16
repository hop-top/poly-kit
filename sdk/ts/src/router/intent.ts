/**
 * Intent-based routing middleware.
 *
 * IntentModelSelector — detects prompt intent and overrides the model
 * pair used for routing. IntentDetector — cosine-similarity-based
 * intent detection using embeddings.
 *
 * Port of Python's intent_model_selector.py and domain_intent_detector.py.
 */

import type { Middleware, ModelPair } from "./router";

// ─── Types ───────────────────────────────────────────────────────────────────

/** Maps an intent name to a specific model pair. */
export interface IntentModelMapping {
  intent: string;
  modelPair: ModelPair;
  description: string;
}

/** Function that classifies a prompt into an intent string. */
export type IntentDetectFn = (prompt: string) => Promise<string>;

/** Function that returns an embedding vector for text. */
export type EmbedFn = (text: string) => Promise<number[]>;

// ─── IntentModelSelector (middleware) ────────────────────────────────────────

export interface IntentModelSelectorOptions {
  /** Intent-to-model mappings. */
  mappings: IntentModelMapping[];
  /** Default model pair when no intent matches. */
  defaultPair: ModelPair;
  /** Intent detection function. */
  detect: IntentDetectFn;
}

/**
 * Middleware that selects a model pair based on detected intent.
 *
 * If the detected intent matches a mapping, returns that mapping's
 * model pair. Otherwise returns the default pair.
 */
export class IntentModelSelector implements Middleware {
  private readonly _lookup: Map<string, ModelPair>;
  private readonly _defaultPair: ModelPair;
  private readonly _detect: IntentDetectFn;
  private readonly _cache: Map<string, string>;

  constructor(opts: IntentModelSelectorOptions) {
    this._lookup = new Map(
      opts.mappings.map((m) => [m.intent, m.modelPair]),
    );
    this._defaultPair = opts.defaultPair;
    this._detect = opts.detect;
    this._cache = new Map();
  }

  async getModelPair(prompt: string): Promise<ModelPair | null> {
    let intent = this._cache.get(prompt);
    if (intent === undefined) {
      intent = await this._detect(prompt);
      if (this._cache.size >= 10_000) this._cache.clear();
      this._cache.set(prompt, intent);
    }

    return this._lookup.get(intent) ?? this._defaultPair;
  }

  /** Return all registered intent names. */
  intents(): string[] {
    return [...this._lookup.keys()];
  }

  /** Clear the detection cache. */
  clearCache(): void {
    this._cache.clear();
  }
}

// ─── Cosine similarity helpers ───────────────────────────────────────────────

/** Dot product of two equal-length vectors. */
export function dot(a: number[], b: number[]): number {
  let sum = 0;
  for (let i = 0; i < a.length; i++) sum += a[i] * b[i];
  return sum;
}

/** L2 norm of a vector. */
export function norm(v: number[]): number {
  let sum = 0;
  for (let i = 0; i < v.length; i++) sum += v[i] * v[i];
  return Math.sqrt(sum);
}

/** Cosine similarity between two vectors. */
export function cosineSimilarity(a: number[], b: number[]): number {
  const nA = norm(a);
  const nB = norm(b);
  if (nA === 0 || nB === 0) return 0;
  return dot(a, b) / (nA * nB);
}

// ─── IntentDetector (embedding-based) ────────────────────────────────────────

export interface IntentDetectorOptions {
  /** Function to get embedding vectors. */
  embed: EmbedFn;
}

/**
 * Embedding-based intent detector.
 *
 * Uses cosine similarity between the prompt embedding and stored
 * example embeddings to find the best-matching intent.
 */
export class IntentDetector {
  private readonly _embed: EmbedFn;
  private readonly _examples: Map<string, number[][]>;

  constructor(opts: IntentDetectorOptions) {
    this._embed = opts.embed;
    this._examples = new Map();
  }

  /**
   * Add example embeddings for an intent.
   *
   * Computes embeddings for the given texts and stores them.
   */
  async addExamples(intent: string, texts: string[]): Promise<void> {
    const existing = this._examples.get(intent) ?? [];
    const newEmbs = await Promise.all(texts.map((t) => this._embed(t)));
    this._examples.set(intent, [...existing, ...newEmbs]);
  }

  /**
   * Detect the intent of a prompt.
   *
   * Returns the intent with the highest average cosine similarity
   * to the prompt. Returns "general" if no examples are stored.
   */
  async detect(prompt: string): Promise<string> {
    if (this._examples.size === 0) return "general";

    const promptEmb = await this._embed(prompt);
    let bestIntent = "general";
    let bestScore = -Infinity;

    for (const [intent, embeddings] of this._examples) {
      if (embeddings.length === 0) continue;

      let sum = 0;
      for (const emb of embeddings) {
        sum += cosineSimilarity(promptEmb, emb);
      }
      const avg = sum / embeddings.length;

      if (avg > bestScore) {
        bestScore = avg;
        bestIntent = intent;
      }
    }

    return bestIntent;
  }

  /**
   * Get confidence scores for all intents.
   *
   * Returns a map from intent to normalized confidence score.
   */
  async confidence(prompt: string): Promise<Map<string, number>> {
    const result = new Map<string, number>();
    if (this._examples.size === 0) return result;

    const promptEmb = await this._embed(prompt);
    let total = 0;

    for (const [intent, embeddings] of this._examples) {
      if (embeddings.length === 0) continue;

      let sum = 0;
      for (const emb of embeddings) {
        sum += cosineSimilarity(promptEmb, emb);
      }
      const score = Math.max(0, sum / embeddings.length);
      result.set(intent, score);
      total += score;
    }

    // Normalize to proper confidence distribution.
    if (total > 0) {
      for (const [k, v] of result) {
        result.set(k, v / total);
      }
    } else if (result.size > 0) {
      const fallback = 1 / result.size;
      for (const k of result.keys()) {
        result.set(k, fallback);
      }
    }

    return result;
  }
}
