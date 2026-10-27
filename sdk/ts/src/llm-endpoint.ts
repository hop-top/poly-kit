/**
 * Where credentials may go: URI redaction, endpoint parsing, the
 * plain-http and trusted-origin gates, and adapter declarations.
 * Re-exported from `@hop-top/kit/llm`; mirrors Go `llm` (`RedactURI`,
 * `CheckKeyEndpoint`, `CredentialsTrusted`).
 *
 * @internal
 */

import { KeyEndpointError } from "./llm-errors";
import { isLoopback } from "./netpolicy";

// ─── Redaction ───────────────────────────────────────────────────────────────

const REDACTED = "REDACTED";

/**
 * Masks the credentials in a provider URI or URL: the value of every
 * credential-named query param (`api_key`, `key`, `*_token`, ...) and
 * userinfo before an `@`. Tolerates malformed input; a `name@version`
 * model id is kept.
 */
export function redactURI(raw: string): string {
  let out = "";
  for (;;) {
    const m = /[?&]/.exec(raw);
    if (!m) return out + redactSegment(raw);
    out += redactSegment(raw.slice(0, m.index)) + m[0];
    raw = raw.slice(m.index + 1);
  }
}

function redactSegment(seg: string): string {
  const eq = seg.indexOf("=");
  if (eq < 0) return redactUserinfo(seg, false);
  const k = seg.slice(0, eq);
  const v = seg.slice(eq + 1);
  if (k.includes("://")) return redactUserinfo(seg, false);
  if (credentialParam(k)) return v === "" ? seg : `${k}=${REDACTED}`;
  return `${k}=${redactUserinfo(v, true)}`;
}

function redactUserinfo(s: string, strict: boolean): string {
  let start = 0;
  let scheme = "";
  const i = s.indexOf("://");
  if (i >= 0) {
    start = i + 3;
    scheme = s.slice(0, i).toLowerCase();
  }
  const rest = s.slice(start);
  let end = rest.indexOf("/");
  const hasPath = end >= 0;
  if (!hasPath) end = rest.length;
  const at = rest.slice(0, end).lastIndexOf("@");
  if (at <= 0) return s;
  const credential =
    strict ||
    scheme === "http" ||
    scheme === "https" ||
    (hasPath && start > 0) ||
    rest.slice(0, at).includes(":");
  if (!credential) return s; // a "name@version" model id
  return s.slice(0, start) + REDACTED + rest.slice(at);
}

const CREDENTIAL_NAMES = new Set([
  "key", "apikey", "token", "secret", "password", "passwd", "pwd",
  "auth", "authorization", "signature", "sig", "credential", "credentials",
]);
const CREDENTIAL_SUFFIXES = [
  "_key", "apikey", "_token", "_secret", "_password",
  "_signature", "_credential", "_credentials",
];

function credentialParam(name: string): boolean {
  try {
    name = decodeURIComponent(name.replace(/\+/g, " "));
  } catch {
    // keep the raw name
  }
  name = name.trim().toLowerCase().replace(/-/g, "_");
  if (CREDENTIAL_NAMES.has(name)) return true;
  return CREDENTIAL_SUFFIXES.some((s) => name.endsWith(s));
}

/**
 * Returns `msg` with every secret replaced by `REDACTED`. Blank secrets
 * are ignored.
 */
export function scrubSecrets(msg: string, secrets: readonly string[]): string {
  for (const s of secrets) {
    if (s.trim() !== "") msg = msg.split(s).join(REDACTED);
  }
  return msg;
}

// ─── Endpoints ───────────────────────────────────────────────────────────────

interface Endpoint {
  scheme: string;
  host: string;
  port: string;
}

/**
 * Parses `raw` for a credential check with the WHATWG URL parser fetch
 * uses. Undefined, and no credential may go to `raw`, when it does not
 * parse, has no host, or carries userinfo, a backslash, a
 * percent-escape, whitespace or a control character: forms where
 * parsers disagree on the host.
 */
function parseEndpoint(raw: string): Endpoint | undefined {
  if (/[\\%]/.test(raw) || /[\u0000- \u007f\s]/.test(raw)) {
    return undefined;
  }
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return undefined;
  }
  const authority = raw.slice(raw.indexOf("//") + 2).split(/[/?#]/)[0] ?? "";
  if (!u.host || !u.hostname || u.username || u.password || authority.includes("@")) {
    return undefined;
  }
  const scheme = u.protocol.replace(/:$/, "").toLowerCase();
  let port = u.port;
  if (port === "") {
    if (scheme === "https") port = "443";
    else if (scheme === "http") port = "80";
  }
  return { scheme, host: u.hostname.toLowerCase(), port };
}

function origin(e: Endpoint): string {
  return `${e.scheme}://${e.host}:${e.port}`;
}

function blankKey(key: string | undefined): boolean {
  return (key ?? "").trim() === "";
}

/**
 * Throws a {@link KeyEndpointError} for a non-blank `key` bound for a
 * malformed endpoint, or over plain http to a host other than loopback.
 * Adapters call it on the base URL they settle on.
 */
export function checkKeyEndpoint(
  scheme: string,
  key: string | undefined,
  baseURL: string
): void {
  if (blankKey(key) || baseURL === "") return;
  const e = parseEndpoint(baseURL);
  if (!e) {
    throw new KeyEndpointError(scheme, redactURI(baseURL), { malformed: true });
  }
  if (e.scheme === "http" && !isLoopback(e.host)) {
    throw new KeyEndpointError(scheme, redactURI(baseURL), { insecure: true });
  }
}

// ─── Declarations ────────────────────────────────────────────────────────────

/**
 * What an adapter states about its scheme: the endpoints credentials
 * kit resolved itself may go to (see {@link credentialsTrusted}).
 */
export interface Declaration {
  endpoints?: string[];
}

const declarations = new Map<string, Declaration>();

/** @internal Records `scheme`'s declaration; `register` calls it. */
export function setDeclaration(scheme: string, decl: Declaration): void {
  declarations.set(scheme, decl);
}

/** @internal */
export function clearDeclarations(): void {
  declarations.clear();
}

/** The declaration `scheme` registered with, if any. */
export function declarationOf(scheme: string): Declaration | undefined {
  return declarations.get(scheme);
}

/**
 * Reports whether credentials kit resolved itself, not taken from the
 * URI (an env key, a bearer token, a workspace header), may go to
 * `baseURL` for `scheme`: it is loopback, or shares an origin with the
 * scheme's declared endpoints, `LLM_BASE_URL` or one of `extra` (the
 * adapter's default endpoint and its own base URL variable).
 */
export function credentialsTrusted(
  scheme: string,
  baseURL: string,
  ...extra: string[]
): boolean {
  return trustedOrigin(scheme, baseURL, extra, process.env["LLM_BASE_URL"]);
}

/**
 * @internal {@link credentialsTrusted} with `LLM_BASE_URL` passed in:
 * a hermetic adapter passes none.
 */
export function trustedOrigin(
  scheme: string,
  baseURL: string,
  extra: readonly string[],
  llmBaseURL: string | undefined
): boolean {
  const e = parseEndpoint(baseURL);
  if (!e) return false;
  if (isLoopback(e.host)) return true;
  const trusted = [...(declarations.get(scheme)?.endpoints ?? []), ...extra];
  if (llmBaseURL) trusted.push(llmBaseURL);
  return trusted.some((t) => {
    const te = t ? parseEndpoint(t) : undefined;
    return te !== undefined && origin(te) === origin(e);
  });
}
