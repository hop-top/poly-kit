/**
 * The guarded fetch behind `urlSource(url).read()`.
 *
 * A media URL may come from untrusted input, so the fetch refuses what
 * would make it a server-side request forgery or a local file read:
 *
 * - `http` and `https` only (no `file:`, `ftp:`, `data:`), no userinfo;
 * - every address the host resolves to must be public: loopback,
 *   private, link-local (cloud metadata `169.254.169.254` among them),
 *   shared, documentation, reserved, unspecified and multicast
 *   addresses are refused, v4-mapped and NAT64 IPv6 judged by their
 *   IPv4;
 * - the socket connects to an address the check passed (the lookup
 *   handed to `node:http` is the vetted one), so a second DNS answer
 *   cannot rebind it; TLS still verifies the host name;
 * - no proxy, no redirects (a 3xx is an `HTTPStatusError`); a non-2xx
 *   status is an error;
 * - the body is capped ({@link DEFAULT_MAX_MEDIA_BYTES}, or `maxBytes`),
 *   30s for the whole download;
 * - the `--offline` policy (`netpolicy`) applies.
 *
 * Errors name the destination without query or userinfo. There is no
 * switch to allow private hosts: media on a local or private host is
 * read by the caller and passed as `inlineSource` or `fileSource`.
 *
 * @internal
 */

import * as dns from "node:dns";
import * as http from "node:http";
import * as https from "node:https";
import { isIP, type LookupFunction } from "node:net";

import { LLMError } from "./llm-error";
import { HTTPStatusError } from "./llm-errors";
import { OfflineError, isLoopback, isOffline } from "./netpolicy";

/** A URL media download is capped at this many bytes unless told otherwise: 32 MiB. */
export const DEFAULT_MAX_MEDIA_BYTES = 32 << 20;

const TIMEOUT_MS = 30_000;

/**
 * A URL media source refused to fetch its URL: a scheme other than
 * http(s), userinfo, no host, or an address that is not public.
 * `destination` is the URL without query or userinfo.
 */
export class MediaURLRefusedError extends LLMError {
  readonly destination: string;
  readonly reason: string;

  constructor(destination: string, reason: string) {
    super(`media: refusing to fetch ${destination}: ${reason}`);
    this.name = "MediaURLRefusedError";
    this.destination = destination;
    this.reason = reason;
  }
}

/** Media over its size limit. */
export class MediaTooLargeError extends LLMError {
  readonly size: number;
  readonly limit: number;
  readonly provider: string;

  constructor(size: number, limit: number, provider: string) {
    super(`media too large (${size} bytes, limit ${limit}) for provider ${JSON.stringify(provider)}`);
    this.name = "MediaTooLargeError";
    this.size = size;
    this.limit = limit;
    this.provider = provider;
  }
}

// ---------------------------------------------------------------------------
// Addresses
// ---------------------------------------------------------------------------

type Prefix = { bytes: number[]; bits: number };

function prefix(cidr: string): Prefix {
  const [addr, bits] = cidr.split("/");
  const bytes = addressBytes(addr);
  if (!bytes) throw new Error(`bad prefix ${cidr}`);
  return { bytes, bits: Number(bits) };
}

/** Special-purpose ranges (IANA registries) that are not public. */
const NON_PUBLIC_V4 = [
  "0.0.0.0/8",
  "10.0.0.0/8",
  "100.64.0.0/10",
  "127.0.0.0/8",
  "169.254.0.0/16",
  "172.16.0.0/12",
  "192.0.0.0/24",
  "192.0.2.0/24",
  "192.168.0.0/16",
  "198.18.0.0/15",
  "198.51.100.0/24",
  "203.0.113.0/24",
  "224.0.0.0/4",
  "240.0.0.0/4",
].map(prefix);

const NON_PUBLIC_V6 = [
  "::/96",
  "64:ff9b:1::/48",
  "100::/64",
  "2001::/23",
  "2001:db8::/32",
  "2002::/16",
  "3fff::/20",
  "5f00::/16",
  "fc00::/7",
  "fe80::/10",
  "fec0::/10",
  "ff00::/8",
].map(prefix);

const MAPPED = prefix("::ffff:0:0/96");
const NAT64 = prefix("64:ff9b::/96");

function contains(p: Prefix, b: number[]): boolean {
  if (p.bytes.length !== b.length) return false;
  let bits = p.bits;
  for (let i = 0; bits > 0; i++, bits -= 8) {
    const mask = bits >= 8 ? 0xff : (0xff << (8 - bits)) & 0xff;
    if ((p.bytes[i] & mask) !== (b[i] & mask)) return false;
  }
  return true;
}

/** The bytes of an IPv4 or IPv6 literal (zone dropped), or undefined. */
function addressBytes(addr: string): number[] | undefined {
  const a = addr.replace(/%.*$/, "");
  const kind = isIP(a);
  if (kind === 4) return a.split(".").map(Number);
  if (kind !== 6) return undefined;
  // Let WHATWG URL canonicalise: an embedded dotted quad becomes two
  // hex groups, so only "::" compression is left to expand.
  const canon = new URL(`http://[${a}]/`).hostname.slice(1, -1);
  const [head, tail] = canon.includes("::") ? canon.split("::") : [canon, undefined];
  const h = head ? head.split(":") : [];
  const t = tail ? tail.split(":") : [];
  const groups = tail === undefined ? h : [...h, ...Array(8 - h.length - t.length).fill("0"), ...t];
  return groups.flatMap((g) => {
    const n = parseInt(g, 16);
    return [n >> 8, n & 0xff];
  });
}

/**
 * Whether addr (an IPv4 or IPv6 literal) is a globally routable unicast
 * address. A v4-mapped or NAT64 (64:ff9b::/96) address is judged by the
 * IPv4 address it carries. Mirrors Go's guard.
 */
export function isPublicAddress(addr: string): boolean {
  let b = addressBytes(addr);
  if (!b) return false;
  if (b.length === 16 && (contains(MAPPED, b) || contains(NAT64, b))) b = b.slice(12);
  if (b.length === 4) {
    if (b.every((x) => x === 255)) return false;
    return !NON_PUBLIC_V4.some((p) => contains(p, b));
  }
  return !NON_PUBLIC_V6.some((p) => contains(p, b));
}

// ---------------------------------------------------------------------------
// Test seams
// ---------------------------------------------------------------------------

type Resolver = (host: string) => Promise<string[]>;

const systemResolver: Resolver = async (host) =>
  (await dns.promises.lookup(host, { all: true, verbatim: true })).map((a) => a.address);

let addressAllowed: (addr: string) => boolean = isPublicAddress;
let resolve: Resolver = systemResolver;

/**
 * Test hook: replace the address check and the resolver until the
 * returned restore function runs. Not part of the public API.
 *
 * @internal
 */
export function _setMediaFetchSeams(seams: {
  allow?: (addr: string, isPublic: (a: string) => boolean) => boolean;
  resolve?: Resolver;
}): () => void {
  const prev = { addressAllowed, resolve };
  if (seams.allow) {
    const allow = seams.allow;
    addressAllowed = (a) => allow(a, isPublicAddress);
  }
  if (seams.resolve) resolve = seams.resolve;
  return () => {
    addressAllowed = prev.addressAllowed;
    resolve = prev.resolve;
  };
}

// ---------------------------------------------------------------------------
// Fetch
// ---------------------------------------------------------------------------

/**
 * Render url as scheme, host and path only (mirrors Go's guard): query,
 * fragment and userinfo may carry credentials. An opaque URL keeps its
 * scheme alone; one that does not parse is not echoed.
 */
export function mediaDestination(url: string): string {
  let u: URL;
  try {
    u = new URL(url);
  } catch {
    return "<unparseable URL>";
  }
  if (u.host === "" && !u.pathname.startsWith("/")) return u.protocol;
  return `${u.protocol}//${u.host}${u.pathname}`;
}

/** Resolve host once and refuse it unless every address is allowed. */
async function vettedAddresses(dest: string, host: string): Promise<{ address: string; family: number }[]> {
  const literal = isIP(host.replace(/%.*$/, ""));
  const addrs = literal ? [host] : await resolve(host);
  if (addrs.length === 0) throw new MediaURLRefusedError(dest, "host resolves to no address");
  for (const a of addrs) {
    if (!addressAllowed(a)) throw new MediaURLRefusedError(dest, `non-public address ${a}`);
  }
  return addrs.map((a) => ({ address: a, family: isIP(a.replace(/%.*$/, "")) }));
}

/**
 * GET url under the rules in the module docs and return its body.
 * `maxBytes` caps the body (default {@link DEFAULT_MAX_MEDIA_BYTES}).
 */
export async function fetchMedia(url: string, maxBytes: number = DEFAULT_MAX_MEDIA_BYTES): Promise<Uint8Array> {
  const dest = mediaDestination(url);
  let u: URL;
  try {
    u = new URL(url);
  } catch {
    throw new MediaURLRefusedError(dest, "URL does not parse");
  }
  if (u.protocol !== "http:" && u.protocol !== "https:") {
    throw new MediaURLRefusedError(dest, "only http or https URLs are fetched");
  }
  if (u.username !== "" || u.password !== "") throw new MediaURLRefusedError(dest, "URL carries userinfo");
  // URL keeps IPv6 brackets in hostname.
  const host = u.hostname.replace(/^\[(.*)\]$/, "$1");
  if (host === "") throw new MediaURLRefusedError(dest, "URL has no host");
  if (isOffline() && !isLoopback(host)) throw new OfflineError("GET", url);
  const addrs = await vettedAddresses(dest, host);

  // node:http calls lookup for the host; answer with the vetted
  // addresses, never a fresh resolution. A literal host skips lookup.
  const lookup: LookupFunction = (_hostname, options, cb) => {
    const fam = typeof options === "object" ? options.family : undefined;
    const pick = addrs.filter((a) => !fam || a.family === fam);
    if (pick.length === 0) {
      cb(Object.assign(new Error(`no ${fam === 6 ? "IPv6" : "IPv4"} address`), { code: "ENOTFOUND" }), "", 0);
      return;
    }
    if (typeof options === "object" && options.all) {
      (cb as unknown as (e: null, a: { address: string; family: number }[]) => void)(null, pick);
    } else {
      cb(null, pick[0].address, pick[0].family);
    }
  };

  const mod = u.protocol === "https:" ? https : http;
  return await new Promise<Uint8Array>((resolveBody, reject) => {
    const req = mod.request(
      u,
      {
        method: "GET",
        headers: { accept: "*/*" },
        lookup,
        agent: false,
        timeout: TIMEOUT_MS,
      },
      (res) => {
        const status = res.statusCode ?? 0;
        const fail = (err: Error) => {
          res.destroy();
          reject(err);
        };
        if (status >= 300 && status < 400) {
          fail(new HTTPStatusError(status, `media: redirect not followed (${dest})`));
          return;
        }
        if (status < 200 || status >= 300) {
          fail(new LLMError(`media: fetch ${dest}: http ${status}`));
          return;
        }
        const declared = Number(res.headers["content-length"]);
        if (Number.isFinite(declared) && declared > maxBytes) {
          fail(new MediaTooLargeError(declared, maxBytes, "media"));
          return;
        }
        const chunks: Buffer[] = [];
        let n = 0;
        res.on("data", (c: Buffer) => {
          n += c.length;
          if (n > maxBytes) {
            fail(new MediaTooLargeError(n, maxBytes, "media"));
            return;
          }
          chunks.push(c);
        });
        res.on("end", () => resolveBody(new Uint8Array(Buffer.concat(chunks))));
        res.on("error", (e) => reject(new LLMError(`media: fetch ${dest}: ${e.message}`)));
      },
    );
    const deadline = setTimeout(() => req.destroy(new Error("timeout")), TIMEOUT_MS);
    req.on("close", () => clearTimeout(deadline));
    req.on("timeout", () => req.destroy(new Error("timeout")));
    req.on("error", (e) => reject(e instanceof LLMError ? e : new LLMError(`media: fetch ${dest}: ${e.message}`)));
    req.end();
  });
}
