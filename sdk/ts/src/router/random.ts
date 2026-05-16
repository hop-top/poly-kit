/**
 * RandomRouter — returns a uniform random score in [0, 1].
 *
 * Useful for A/B testing and baseline comparisons.
 */

import type { Router } from "./router";

export class RandomRouter implements Router {
  async score(_prompt: string): Promise<number> {
    return Math.random();
  }
}
