/**
 * Lenient semver for migration versions, ported from Go `core/upgrade`.
 *
 * Not the strict `semver` package: a leading `v` is dropped, build metadata
 * after `+` is ignored, and each of major, minor and patch is the leading
 * decimal digits of its dot-separated part (missing or non-numeric is 0;
 * anything after the third dot belongs to patch and is ignored). A release
 * sorts above its pre-releases; pre-release identifiers compare left to
 * right, a numeric identifier (decimal, no leading zero) below an
 * alphanumeric one, numbers by value, text bytewise, and a shorter list of
 * equal identifiers first. Nothing here throws: any string parses.
 */

/** A parsed lenient version. `pre` is empty for a release. */
export interface Semver {
  major: number;
  minor: number;
  patch: number;
  pre: string;
}

function leadingInt(s: string): number {
  const m = /^\d+/.exec(s);
  return m ? Number(m[0]) : 0;
}

/** Parses `s` leniently. A leading `v` is NOT dropped here; see {@link compareVersions}. */
export function parseSemver(s: string): Semver {
  const v: Semver = { major: 0, minor: 0, patch: 0, pre: '' };
  let rest = s;
  const plus = rest.indexOf('+');
  if (plus >= 0) rest = rest.slice(0, plus);
  const dash = rest.indexOf('-');
  if (dash >= 0) {
    v.pre = rest.slice(dash + 1);
    rest = rest.slice(0, dash);
  }
  const parts = rest.split('.');
  v.major = leadingInt(parts[0] ?? '');
  v.minor = leadingInt(parts[1] ?? '');
  // Everything after the second dot is the patch part; only its leading digits count.
  v.patch = leadingInt(parts.slice(2).join('.'));
  return v;
}

function sign(n: number): -1 | 0 | 1 {
  return n < 0 ? -1 : n > 0 ? 1 : 0;
}

const NUMERIC = /^(0|[1-9]\d*)$/;

function comparePreIdent(a: string, b: string): number {
  const aNum = NUMERIC.test(a);
  const bNum = NUMERIC.test(b);
  if (aNum && bNum) return sign(Number(a) - Number(b));
  if (aNum) return -1;
  if (bNum) return 1;
  return Buffer.compare(Buffer.from(a, 'utf8'), Buffer.from(b, 'utf8'));
}

function comparePreRelease(a: string, b: string): number {
  const ap = a.split('.');
  const bp = b.split('.');
  const n = Math.min(ap.length, bp.length);
  for (let i = 0; i < n; i++) {
    const d = comparePreIdent(ap[i], bp[i]);
    if (d !== 0) return d;
  }
  return sign(ap.length - bp.length);
}

/** Orders two parsed versions: -1, 0 or 1. */
export function compareSemver(a: Semver, b: Semver): -1 | 0 | 1 {
  const d = a.major - b.major || a.minor - b.minor || a.patch - b.patch;
  if (d !== 0) return sign(d);
  if (a.pre === '' && b.pre === '') return 0;
  if (a.pre === '') return 1;
  if (b.pre === '') return -1;
  return sign(comparePreRelease(a.pre, b.pre));
}

/** Orders two version strings after dropping one leading `v`: -1, 0 or 1. */
export function compareVersions(a: string, b: string): -1 | 0 | 1 {
  return compareSemver(parseSemver(trimV(a)), parseSemver(trimV(b)));
}

/** Drops one leading `v`. */
export function trimV(s: string): string {
  return s.startsWith('v') ? s.slice(1) : s;
}
