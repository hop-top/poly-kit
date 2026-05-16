/**
 * Controller — router registry, threshold parsing, model routing.
 *
 * Manages a set of named routers and middleware. Routes prompts by
 * scoring them against a router and comparing to a threshold.
 */

import {
  type Router,
  type Middleware,
  type ModelPair,
  RoutingError,
  route,
} from "./router";

// ─── Types ───────────────────────────────────────────────────────────────────

export interface ControllerOptions {
  /** Default strong model name. */
  strongModel: string;
  /** Default weak model name. */
  weakModel: string;
  /** Named routers to register. */
  routers?: Record<string, Router>;
  /** Middleware applied in order before routing. */
  middleware?: Middleware[];
}

/** Parsed model string: "router-<name>-<threshold>". */
export interface ParsedModelName {
  routerName: string;
  threshold: number;
}

// ─── Controller ──────────────────────────────────────────────────────────────

export class Controller {
  readonly defaultPair: ModelPair;
  private readonly _routers: Map<string, Router>;
  private readonly _middleware: Middleware[];
  /** Per-router, per-model call counts (for observability). */
  readonly modelCounts: Map<string, Map<string, number>>;

  constructor(opts: ControllerOptions) {
    this.defaultPair = {
      strong: opts.strongModel,
      weak: opts.weakModel,
    };
    this._routers = new Map(Object.entries(opts.routers ?? {}));
    this._middleware = opts.middleware ?? [];
    this.modelCounts = new Map();
  }

  /** Register a router under a name. */
  addRouter(name: string, router: Router): void {
    this._routers.set(name, router);
  }

  /** List registered router names. */
  routerNames(): string[] {
    return [...this._routers.keys()];
  }

  /**
   * Route a prompt: score it with the named router and pick a model.
   *
   * @returns The chosen model name (strong or weak).
   */
  async route(
    prompt: string,
    routerName: string,
    threshold: number,
  ): Promise<string> {
    this._validate(routerName, threshold);

    const pair = await this._resolveModelPair(prompt);
    const router = this._routers.get(routerName)!;
    const winRate = await router.score(prompt);
    const model = route(winRate, threshold, pair);

    // Track counts.
    if (!this.modelCounts.has(routerName)) {
      this.modelCounts.set(routerName, new Map());
    }
    const counts = this.modelCounts.get(routerName)!;
    counts.set(model, (counts.get(model) ?? 0) + 1);

    return model;
  }

  /**
   * Route using a model string of the form "router-<name>-<threshold>".
   *
   * This mirrors the Python server's model-name convention.
   */
  async routeByModelName(
    prompt: string,
    modelName: string,
  ): Promise<string> {
    const { routerName, threshold } = parseModelName(modelName);
    return this.route(prompt, routerName, threshold);
  }

  // ── Helpers ──────────────────────────────────────────────────────

  private _validate(routerName: string, threshold: number): void {
    if (!this._routers.has(routerName)) {
      throw new RoutingError(
        `Invalid router "${routerName}". ` +
          `Available: ${this.routerNames().join(", ")}`,
      );
    }
    if (threshold < 0 || threshold > 1) {
      throw new RoutingError(
        `Invalid threshold ${threshold}. Must be in [0, 1].`,
      );
    }
  }

  private async _resolveModelPair(prompt: string): Promise<ModelPair> {
    let pair = this.defaultPair;
    for (const mw of this._middleware) {
      const override = await mw.getModelPair(prompt);
      if (override) pair = override;
    }
    return pair;
  }
}

// ─── Model name parser ──────────────────────────────────────────────────────

/**
 * Parse a model name of the form "router-<name>-<threshold>".
 *
 * Example: "router-mf-0.7" => { routerName: "mf", threshold: 0.7 }
 */
export function parseModelName(model: string): ParsedModelName {
  if (!model.startsWith("router-")) {
    throw new RoutingError(
      `Invalid model "${model}". ` +
        `Expected format: router-<name>-<threshold>.`,
    );
  }

  const rest = model.slice("router-".length);
  const lastDash = rest.lastIndexOf("-");
  if (lastDash < 1) {
    throw new RoutingError(
      `Invalid model "${model}". ` +
        `Expected format: router-<name>-<threshold>.`,
    );
  }

  const routerName = rest.slice(0, lastDash);
  const rawThreshold = rest.slice(lastDash + 1);
  const threshold = Number(rawThreshold);

  if (Number.isNaN(threshold)) {
    throw new RoutingError(
      `Invalid threshold "${rawThreshold}" in model "${model}".`,
    );
  }

  return { routerName, threshold };
}
