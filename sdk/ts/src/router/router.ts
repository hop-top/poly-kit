/**
 * Core router interfaces and types for RouteLLM routing.
 *
 * Defines the Router interface (score-based routing), Middleware protocol,
 * ModelPair type, and RoutingError.
 */

// ─── Types ───────────────────────────────────────────────────────────────────

/** A pair of models: strong (high-quality) and weak (cost-effective). */
export interface ModelPair {
  strong: string;
  weak: string;
}

// ─── Errors ──────────────────────────────────────────────────────────────────

/** Error thrown when routing fails (bad config, missing router, etc.). */
export class RoutingError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "RoutingError";
  }
}

// ─── Router interface ────────────────────────────────────────────────────────

/**
 * A Router scores a prompt to decide which model to use.
 *
 * score() returns a value in [0, 1] representing the estimated win rate
 * of the strong model. If score >= threshold, the controller routes to
 * the strong model; otherwise, the weak model.
 */
export interface Router {
  /** Returns a score in [0, 1] — the strong-model win rate estimate. */
  score(prompt: string): Promise<number>;
}

/**
 * Route helper — applies the threshold decision to a score.
 *
 * If score >= threshold, returns pair.strong; otherwise pair.weak.
 */
export function route(
  score: number,
  threshold: number,
  pair: ModelPair,
): string {
  return score >= threshold ? pair.strong : pair.weak;
}

// ─── Middleware interface ────────────────────────────────────────────────────

/**
 * Middleware can override the model pair before routing.
 *
 * Returns null to keep the default pair, or a ModelPair to override it.
 */
export interface Middleware {
  getModelPair(prompt: string): Promise<ModelPair | null>;
}
