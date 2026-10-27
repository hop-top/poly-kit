/**
 * Key resolution for the registry factories of the HTTP adapters: the
 * scheme's env key when the URI gives none, and the gate that keeps an
 * env key off an endpoint only the URI names.
 *
 * @internal
 */

import type { ResolvedConfig } from "./llm";
import { credentialsTrusted, redactURI } from "./llm-endpoint";
import { KeyEndpointError } from "./llm-errors";

/** `<SCHEME>_API_KEY`: `openrouter` reads `OPENROUTER_API_KEY`. */
export function envKeyName(scheme: string): string {
  return `${scheme.toUpperCase().replace(/[^A-Z0-9]/g, "_")}_API_KEY`;
}

/**
 * Returns `cfg` with the provider key resolved: the configured key
 * (URI `api_key` or `LLM_API_KEY`), else `envVar` when given and set.
 *
 * A key the URI itself carries is the caller's choice for its endpoint.
 * Any other key reaches an endpoint only the URI names (its `base_url`
 * or host) only when that endpoint is loopback or shares an origin with
 * `trusted`, the scheme's declared endpoints or `LLM_BASE_URL`;
 * otherwise this throws a {@link KeyEndpointError}.
 */
export function withResolvedKey(
  cfg: ResolvedConfig,
  envVar: string | undefined,
  trusted: string[]
): ResolvedConfig {
  let key = cfg.provider.apiKey;
  if ((key ?? "").trim() === "" && envVar) {
    const v = process.env[envVar];
    key = v && v.trim() !== "" ? v : undefined;
  }
  if ((key ?? "").trim() === "") {
    return { ...cfg, provider: { ...cfg.provider, apiKey: undefined } };
  }
  const uriEndpoint =
    cfg.uri.params?.["base_url"] || (cfg.uri.host ? `http://${cfg.uri.host}` : "");
  const fromURI = key === cfg.uri.params?.["api_key"];
  if (
    uriEndpoint &&
    !fromURI &&
    cfg.provider.baseURL === uriEndpoint &&
    !credentialsTrusted(cfg.uri.scheme, uriEndpoint, ...trusted)
  ) {
    throw new KeyEndpointError(cfg.uri.scheme, redactURI(uriEndpoint));
  }
  return { ...cfg, provider: { ...cfg.provider, apiKey: key } };
}
