/**
 * Config loading: llm.yaml, the environment and the URI merged as Go
 * `llm.LoadConfig` merges them. Re-exported from `@hop-top/kit/llm`.
 *
 * @internal
 */

import type { ProviderConfig, ResolvedConfig } from "./llm";
import { checkKeyEndpoint } from "./llm-endpoint";
import { LLMError } from "./llm-error";
import { providerBlock, readLLMFile } from "./llm-file";
import { dropBlankURIKey, guardKeyEndpoint, resolveKey, uriKey } from "./llm-keys";
import { parseURI } from "./llm-uri";

/** Options for {@link loadConfig}, `resolve` and `createLLM`. */
export interface LoadOptions {
  /**
   * Reads nothing ambient: the provider comes from the URI alone
   * (`api_key`, `base_url`, host). No llm.yaml, no `LLM_PROVIDER`,
   * `LLM_FALLBACK`, `LLM_API_KEY`, `LLM_BASE_URL` or `<SCHEME>_API_KEY`,
   * and the adapter gets {@link ResolvedConfig.hermetic}, so it reads no
   * environment variable either. For embedding kit in an app that owns
   * its configuration.
   */
  hermetic?: boolean;
}

/**
 * Builds the {@link ResolvedConfig} for `uriStr` from llm.yaml
 * (`{configDir("hop")}/llm.yaml`), the environment and the URI, the
 * URI highest. Each field, highest precedence first:
 *
 * - URI: `uriStr`; `LLM_PROVIDER`; llm.yaml `default`. None throws.
 * - apiKey: the URI's `api_key`; llm.yaml `providers.<scheme>.api_key`,
 *   then the variable its `api_key_env` names; the scheme's key
 *   variables (`<SCHEME>_API_KEY` unless its adapter declares others);
 *   `LLM_API_KEY` for a registered scheme that requires a key. Blank is
 *   unset everywhere; a blank `api_key` param is dropped. Found
 *   nowhere, it is left unset: the adapter reports it.
 * - baseURL: the URI's `base_url`; its host (`http://host:port`);
 *   `LLM_BASE_URL`; llm.yaml `base_url`.
 * - model: the URI's; llm.yaml `model`. extras: the block's other keys.
 * - fallbacks: `LLM_FALLBACK` (comma-separated); llm.yaml `fallback`.
 *
 * Throws a {@link KeyEndpointError} when the key may not go where the
 * config points: a key not from the URI to an endpoint only the URI
 * names unless loopback or trusted (declared, llm.yaml `base_url`,
 * `LLM_BASE_URL`); any key over plain http to a remote host.
 *
 * With `opts.hermetic` the config is the URI alone (see
 * {@link LoadOptions}).
 */
export function loadConfig(uriStr?: string, opts: LoadOptions = {}): ResolvedConfig {
  if (opts.hermetic === true) return hermeticConfig(uriStr);
  const file = readLLMFile();
  const effective = uriStr || process.env["LLM_PROVIDER"] || file.default;
  if (!effective) {
    throw new LLMError(
      "no LLM URI provided: LLM_PROVIDER is not set and llm.yaml names no default"
    );
  }
  const uri = parseURI(effective);
  dropBlankURIKey(uri);
  const block = providerBlock(file, uri.scheme);

  const key = resolveKey(uri, file);
  guardKeyEndpoint(uri, key.value, key.source);

  const provider: ProviderConfig = { model: uri.model || block?.model || "" };
  if (uri.params) provider.params = { ...uri.params };
  if (block && Object.keys(block.extras).length > 0) provider.extras = block.extras;
  if (key.value !== "") provider.apiKey = key.value;

  // The URI's own choice outranks the environment, which outranks the
  // file.
  let base = process.env["LLM_BASE_URL"] || block?.baseURL || "";
  if (uri.host) base = `http://${uri.host}`;
  if (uri.params && "base_url" in uri.params) base = uri.params["base_url"]!;
  if (base !== "") provider.baseURL = base;
  checkKeyEndpoint(uri.scheme, key.value, base);

  const fbEnv = process.env["LLM_FALLBACK"];
  const fallbacks = fbEnv
    ? fbEnv.split(",").map((s) => s.trim()).filter(Boolean)
    : [...file.fallback];
  return { uri, provider, fallbacks };
}

/** {@link loadConfig} from the URI alone. */
function hermeticConfig(uriStr: string | undefined): ResolvedConfig {
  if (!uriStr) {
    throw new LLMError("no LLM URI provided (hermetic: LLM_PROVIDER is not read)");
  }
  const uri = parseURI(uriStr);
  dropBlankURIKey(uri);
  const provider: ProviderConfig = { model: uri.model };
  if (uri.params) provider.params = { ...uri.params };
  const key = uriKey(uri) ?? "";
  if (key !== "") provider.apiKey = key;
  let base = uri.host ? `http://${uri.host}` : "";
  if (uri.params && "base_url" in uri.params) base = uri.params["base_url"]!;
  if (base !== "") provider.baseURL = base;
  // A key never crosses the network in clear.
  checkKeyEndpoint(uri.scheme, key, base);
  return { uri, provider, fallbacks: [], hermetic: true };
}
