// Runs the routellm_base_url section of contracts/llm-v1/vectors.json:
// the API root the routellm adapter resolves from the provider base URL,
// the router config (extras, ROUTELLM_BASE_URL) and the default. Go is
// canonical; a failure here means this SDK drifted, not that a vector
// should change.

import { afterEach, describe, expect, it } from "vitest";

import { RouteLLMAdapter } from "./routellm";
import { loadVectors, strictKeys } from "../test/llm/conformance-helpers";

interface BaseURLVector {
  name: string;
  base_url: string;
  extras: Record<string, unknown>;
  env: Record<string, string>;
  hermetic: boolean;
  want: string;
}

const vectors = loadVectors<{ routellm_base_url: BaseURLVector[] }>().routellm_base_url;

const saved = process.env["ROUTELLM_BASE_URL"];
afterEach(() => {
  if (saved === undefined) delete process.env["ROUTELLM_BASE_URL"];
  else process.env["ROUTELLM_BASE_URL"] = saved;
});

describe("llm-v1 conformance: routellm_base_url", () => {
  it("has vectors", () => expect(vectors.length).toBeGreaterThan(0));

  it.each(vectors.map((v) => [v.name, v] as const))("%s", (_n, v) => {
    strictKeys(v, ["name", "base_url", "extras", "env", "hermetic", "want"], "routellm_base_url");
    delete process.env["ROUTELLM_BASE_URL"];
    for (const [k, val] of Object.entries(v.env)) process.env[k] = val;
    const a = new RouteLLMAdapter({
      uri: { scheme: "routellm", model: "mf:0.5" },
      provider: { model: "mf:0.5", baseURL: v.base_url || undefined, extras: v.extras },
      fallbacks: [],
      hermetic: v.hermetic,
    });
    expect(a.baseURL).toBe(v.want);
  });
});
