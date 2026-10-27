/**
 * Provider URI parsing, as Go `llm.ParseURI` parses (pinned by
 * contracts/llm-v1 `parse_uri`). Re-exported from `@hop-top/kit/llm`.
 *
 * @internal
 */

import type { URI } from "./llm";
import { redactURI } from "./llm-endpoint";
import { LLMError } from "./llm-error";
import { goQuote } from "./llm-quote";

/**
 * Parses a provider URI: `scheme://[host:port/]model[?params]`.
 *
 * The scheme is required; the model may be empty. The first path
 * segment is a host (`ollama://localhost:11434/llama3`,
 * `ollama://[::1]:11434/llama3`) when it splits as `host:port` with a
 * non-empty port; otherwise the whole path is the model
 * (`openrouter://openai/gpt-4.1-nano`). The query splits on `&`, each
 * pair on its first `=`, and is not percent-decoded; a param without
 * `=` is `""`, one with an empty name is dropped. `params` is absent
 * when none is left.
 *
 * Throws an {@link LLMError} (`empty URI`, `missing scheme in URI
 * "..."`, `empty scheme in URI "..."`) quoting `raw` with its
 * credentials masked by {@link redactURI}.
 */
export function parseURI(raw: string): URI {
  if (raw === "") throw new LLMError("empty URI");
  const idx = raw.indexOf("://");
  if (idx < 0) throw new LLMError(`missing scheme in URI ${goQuote(redactURI(raw))}`);
  const scheme = raw.slice(0, idx);
  if (scheme === "") throw new LLMError(`empty scheme in URI ${goQuote(redactURI(raw))}`);

  let rest = raw.slice(idx + 3);
  let params: Record<string, string> | undefined;
  const q = rest.indexOf("?");
  if (q >= 0) {
    params = parseQuery(rest.slice(q + 1));
    rest = rest.slice(0, q);
  }

  let host = "";
  let model = rest;
  const slash = rest.indexOf("/");
  if (slash > 0) {
    const hp = splitHostPort(rest.slice(0, slash));
    if (hp && hp[1] !== "") {
      host = joinHostPort(hp[0], hp[1]);
      model = rest.slice(slash + 1);
    }
  }

  const uri: URI = { scheme, model };
  if (host !== "") uri.host = host;
  if (params && Object.keys(params).length > 0) uri.params = params;
  return uri;
}

function parseQuery(qs: string): Record<string, string> | undefined {
  if (qs === "") return undefined;
  const m: Record<string, string> = {};
  for (const pair of qs.split("&")) {
    const eq = pair.indexOf("=");
    const k = eq < 0 ? pair : pair.slice(0, eq);
    const v = eq < 0 ? "" : pair.slice(eq + 1);
    if (k === "") continue;
    // An own property even for "__proto__": never the prototype.
    Object.defineProperty(m, k, { value: v, enumerable: true, writable: true, configurable: true });
  }
  return m;
}

/** Go `net.SplitHostPort`: undefined where Go returns an error. */
function splitHostPort(hp: string): [string, string] | undefined {
  const i = hp.lastIndexOf(":");
  if (i < 0) return undefined;
  let host: string;
  let j = 0;
  let k = 0;
  if (hp.startsWith("[")) {
    const end = hp.indexOf("]");
    if (end < 0 || end + 1 !== i) return undefined;
    host = hp.slice(1, end);
    j = 1;
    k = end + 1;
  } else {
    host = hp.slice(0, i);
    if (host.includes(":")) return undefined;
  }
  if (hp.slice(j).includes("[") || hp.slice(k).includes("]")) return undefined;
  return [host, hp.slice(i + 1)];
}

/** Go `net.JoinHostPort`. */
function joinHostPort(host: string, port: string): string {
  return host.includes(":") ? `[${host}]:${port}` : `${host}:${port}`;
}
