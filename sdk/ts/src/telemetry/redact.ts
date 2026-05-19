/**
 * @module telemetry/redact
 * @package @hop-top/kit
 *
 * Best-effort PII/secret redactor for telemetry attrs — the TS mirror
 * of `hop_top_kit/telemetry/redact.py`.
 *
 * NOT parity with `go/core/redact`: this is an opinionated regex set
 * covering common leak shapes (emails, IPs, common token prefixes,
 * $HOME paths). The placeholder strings (`<redacted:email>`,
 * `<redacted:ipv4>`, `<redacted:ipv6>`, `<redacted:token>`) MUST match
 * byte-for-byte across the py / ts / rs / php SDKs so the cross-language
 * contract harness (T-0709) can diff outputs without per-language
 * quirks.
 *
 * Callers can layer their own `redactor` callback on `Client` (T-0720);
 * it runs BEFORE this default pass.
 */
import * as os from 'node:os';

// Pattern order mirrors the py SDK: tokens FIRST (before IP/email) so
// we don't accidentally eat numeric-looking fragments inside an xoxb
// token; $HOME LAST so its tail-capture doesn't swallow an embedded
// email- or ip-shaped substring before the dedicated patterns ran.
const TOKEN_SK = /\bsk-[A-Za-z0-9_-]{8,}\b/g;
const TOKEN_GH = /\bgh[pousr]_[A-Za-z0-9_-]{16,}\b/g;
const TOKEN_XOXB = /\bxoxb-[0-9]+-[0-9]+-[A-Za-z0-9]{24,}\b/g;
const EMAIL = /\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b/g;
// IPv6 regex covers full form (8 groups) AND compressed `::` form
// (1-7 groups + double colon). Matches PHP SDK behavior for cross-lang
// parity. May over-match on hex-heavy strings that resemble IPv6 —
// accepted trade-off (over-redact > leak). The lookbehind/lookahead on
// `[0-9a-fA-F:]` anchors the match so it can't bleed into adjacent
// hex tokens (e.g. a UUID fragment touching a colon).
const IPV6 =
  /(?<![0-9a-fA-F:])(?:(?:[0-9a-fA-F]{1,4}:){1,7}(?::[0-9a-fA-F]{1,4})+|(?:[0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4})(?![0-9a-fA-F:])/g;
const IPV4 = /\b(?:\d{1,3}\.){3}\d{1,3}\b/g;

function escapeRegex(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

const HOME = os.homedir();
// Guard against a missing / unexpanded $HOME (`~`) — emit a pattern
// that never matches so we don't accidentally rewrite the whole input.
const HOME_PATTERN: RegExp =
  HOME && HOME !== '~'
    ? new RegExp(escapeRegex(HOME) + '([/\\w.\\-]*)', 'g')
    : /(?!x)x/g;

const REPLACEMENTS: ReadonlyArray<readonly [RegExp, string]> = [
  [TOKEN_SK, '<redacted:token>'],
  [TOKEN_GH, '<redacted:token>'],
  [TOKEN_XOXB, '<redacted:token>'],
  [EMAIL, '<redacted:email>'],
  [IPV6, '<redacted:ipv6>'],
  [IPV4, '<redacted:ipv4>'],
  [HOME_PATTERN, '$HOME$1'],
];

/**
 * redactString applies every pattern in the opinionated set to `s`.
 * Returns a new string; the input is never mutated.
 */
export function redactString(s: string): string {
  let out = s;
  for (const [pat, repl] of REPLACEMENTS) {
    out = out.replace(pat, repl);
  }
  return out;
}

/**
 * redact returns a NEW value with the opinionated regex set applied to
 * every reachable string. Walks plain objects and arrays recursively.
 * Non-string scalars (number, boolean, null, undefined, bigint, symbol)
 * pass through unchanged. Input is never mutated.
 *
 * NOT parity with `go/core/redact`. Use a per-Client redactor callback
 * for stricter or custom rules; this is the default-on backstop.
 */
export function redact(value: unknown): unknown {
  if (typeof value === 'string') return redactString(value);
  if (Array.isArray(value)) return value.map(redact);
  if (value !== null && typeof value === 'object') {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      out[k] = redact(v);
    }
    return out;
  }
  return value;
}
