/**
 * Provider key resolution and the gate that keeps a key kit resolved
 * itself off an endpoint only the URI names. Mirrors Go `llm`
 * (`ResolveAPIKey`, `ApplyAPIKey`, the key-endpoint guard) without a
 * secret store.
 *
 * @internal
 */

import type { URI } from "./llm";
import {
  blankKey,
  checkKeyEndpoint,
  declarationOf,
  isRegistered,
  redactURI,
  trustedOrigin,
} from "./llm-endpoint";
import { LLMError } from "./llm-error";
import { KeyEndpointError, MissingKeyError } from "./llm-errors";
import { providerBlock, readLLMFile, type LLMFile } from "./llm-file";
import { parseURI } from "./llm-uri";

/** The universal key variable, lent to a scheme that requires a key. */
export const FALLBACK_ENV_KEY = "LLM_API_KEY";

/** `<SCHEME>_API_KEY`: `openrouter` reads `OPENROUTER_API_KEY`. */
export function envKeyName(scheme: string): string {
  return `${scheme.toUpperCase().replace(/[^A-Z0-9]/g, "_")}_API_KEY`;
}

/** Where a resolved key came from. */
export type KeySourceKind = "" | "uri" | "config" | "env" | "fallback";

/** A resolved key; `value` is "" when none was found. */
export interface ResolvedKey {
  value: string;
  source: KeySourceKind;
  /** Set when a required key was found nowhere. */
  missing?: MissingKeyError;
}

/** The URI's own `api_key`; undefined when absent or blank. */
export function uriKey(u: URI): string | undefined {
  const v = u.params?.["api_key"];
  return v !== undefined && !blankKey(v) ? v : undefined;
}

/**
 * Resolves `u`'s key, highest precedence first: the URI's `api_key`;
 * llm.yaml `providers.<scheme>.api_key`, then the variable its
 * `api_key_env` names; the scheme's key variables (its declared
 * {@link ProviderKey} or `<SCHEME>_API_KEY`); `LLM_API_KEY` for a
 * registered scheme that requires a key. A scheme no adapter serves
 * takes only what its own llm.yaml block names. Blank counts as unset
 * everywhere.
 */
export function resolveKey(u: URI, file: LLMFile): ResolvedKey {
  const own = uriKey(u);
  if (own !== undefined) return { value: own, source: "uri" };
  const block = providerBlock(file, u.scheme);
  if (block && !blankKey(block.apiKey)) return { value: block.apiKey, source: "config" };
  const known = isRegistered(u.scheme);
  const decl = declarationOf(u.scheme)?.key;
  let names = known ? [...(decl?.envVars ?? [envKeyName(u.scheme)])] : [];
  if (block && !blankKey(block.apiKeyEnv)) {
    names = [block.apiKeyEnv, ...names.filter((n) => n !== block.apiKeyEnv)];
  }
  if (names.length === 0) return { value: "", source: "" };
  for (const n of names) {
    const v = process.env[n];
    if (v !== undefined && !blankKey(v)) return { value: v, source: "env" };
  }
  const required = known && decl?.optional !== true;
  if (!required) return { value: "", source: "" };
  const fb = process.env[FALLBACK_ENV_KEY];
  if (fb !== undefined && !blankKey(fb)) return { value: fb, source: "fallback" };
  return {
    value: "",
    source: "",
    missing: new MissingKeyError(u.scheme, u.model, [...names, FALLBACK_ENV_KEY]),
  };
}

/** The endpoint a URI itself names: its `base_url`, else its host. */
export function uriEndpoint(u: URI): string {
  const b = u.params?.["base_url"];
  if (b) return b;
  return u.host ? `http://${u.host}` : "";
}

/**
 * Checks that `key`, resolved from `source`, may go where `u` points.
 * A key from the URI itself is the caller's choice for any https host.
 * Any other key goes to a URI-named endpoint only when it is loopback
 * or shares an origin with the scheme's declared endpoints, its llm.yaml
 * `base_url` or `LLM_BASE_URL`. Plain http to a remote host is refused
 * whatever the source. Throws a {@link KeyEndpointError}.
 */
export function guardKeyEndpoint(u: URI, key: string, source: KeySourceKind): void {
  const endpoint = uriEndpoint(u);
  if (blankKey(key) || endpoint === "") return;
  checkKeyEndpoint(u.scheme, key, endpoint);
  if (source === "uri") return;
  if (!trustedOrigin(u.scheme, endpoint, [], true)) {
    throw new KeyEndpointError(u.scheme, redactURI(endpoint));
  }
}

/** Removes a blank `api_key` param from `u`: it is no key. */
export function dropBlankURIKey(u: URI): void {
  const v = u.params?.["api_key"];
  if (v === undefined || !blankKey(v)) return;
  delete u.params!["api_key"];
  if (Object.keys(u.params!).length === 0) delete u.params;
}

/** `raw` without any `name` param; the `?` goes too when none is left. */
function stripURIParam(raw: string, name: string): string {
  const s = raw.indexOf("://");
  if (s < 0) return raw;
  const q = raw.indexOf("?", s + 3);
  if (q < 0) return raw;
  const kept = raw
    .slice(q + 1)
    .split("&")
    .filter((p) => p.split("=", 1)[0] !== name);
  return kept.length === 0 ? raw.slice(0, q) : `${raw.slice(0, q + 1)}${kept.join("&")}`;
}

/**
 * Returns `uri` with the provider's key set as its `api_key` param,
 * resolved as {@link loadConfig} resolves it, after the same endpoint
 * guard. `uri` comes back unchanged when it carries a non-blank
 * `api_key` (a blank one is removed first), when its scheme takes no
 * key, and when no adapter serves the scheme and its llm.yaml block
 * names none. A required key found nowhere throws a
 * {@link MissingKeyError}. Go `ApplyAPIKey` without a secret store.
 *
 * The result holds the key: never log it. Errors never carry it.
 */
export function applyAPIKey(uri: string): string {
  let parsed: URI;
  try {
    parsed = parseURI(uri);
  } catch {
    // The parse error quotes its input, which may carry a key.
    throw new LLMError("llm: apply API key: provider URI must be scheme://model");
  }
  const named = parsed.params?.["api_key"];
  if (named !== undefined && blankKey(named)) {
    uri = stripURIParam(uri, "api_key");
    dropBlankURIKey(parsed);
  }
  const key = resolveKey(parsed, readLLMFile());
  if (key.missing) throw key.missing;
  guardKeyEndpoint(parsed, key.value, key.source);
  if (key.source === "uri" || key.value === "") return uri;
  // The query splits on & and is not unescaped: a key holding a
  // separator cannot travel as a param intact.
  if (/[&?#]/.test(key.value)) {
    throw new LLMError(
      `llm: ${parsed.scheme} API key contains a character a provider URI cannot carry (& ? #)`
    );
  }
  return `${uri}${uri.includes("?") ? "&" : "?"}api_key=${key.value}`;
}
