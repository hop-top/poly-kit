/**
 * @module id
 * @package @hop-top/kit
 *
 * TypeID primitive implementing the cross-language kit API SHAPE per
 * docs/announcements/2026-05-typeid-primitive.md, on the jetify TypeID
 * spec v0.3. Self-contained: no runtime dependency, so no transitive
 * uuid release reaches adopters (typeid-js 1.2.0 pins uuid ^10, under
 * an open advisory). contracts/typeid-v1/fixtures.json and a
 * differential test against typeid-js (dev only) keep it on the wire
 * format.
 *
 * Canonical wire form: `<prefix>_<26-char-base32>` where the suffix is
 * a Crockford-base32-encoded UUIDv7. The TS surface adds a
 * template-literal `Typed<P>` for compile-time prefix safety on top of
 * the runtime checks in {@link parse}.
 *
 * URI composition (e.g. `tlc://task/task_…`) is NOT in this module —
 * callers should use the `@hop-top/cite` package directly. This
 * keeps `kit/id` free of any transitive dependency on the URI
 * registry.
 */

import { z } from 'zod';

// Crockford base32, lowercase, as the TypeID spec defines it.
const ALPHABET = '0123456789abcdefghjkmnpqrstvwxyz';
const DECODE: Record<string, number> = Object.fromEntries(
  [...ALPHABET].map((c, i) => [c, i]),
);
// Spec v0.3: up to 63 chars of [a-z_], starting and ending with [a-z].
const PREFIX_RE = /^(?:[a-z](?:[a-z_]{0,61}[a-z])?)?$/;

function checkPrefix(prefix: string): void {
  if (!PREFIX_RE.test(prefix)) {
    throw new Error(
      `invalid typeid prefix '${prefix}': want up to 63 chars of [a-z_], starting and ending with a letter`,
    );
  }
}

// 16 bytes -> 26 chars: 130 bits, the top two always zero.
function encode(bytes: Uint8Array): string {
  let n = 0n;
  for (const b of bytes) n = (n << 8n) | BigInt(b);
  let out = '';
  for (let i = 0; i < 26; i++) {
    out = ALPHABET[Number(n & 31n)] + out;
    n >>= 5n;
  }
  return out;
}

function decode(suffix: string): Uint8Array {
  if (suffix.length !== 26) {
    throw new Error(`invalid typeid suffix '${suffix}': want 26 chars`);
  }
  if (suffix[0]! > '7') {
    throw new Error(`invalid typeid suffix '${suffix}': first char must be 0-7`);
  }
  let n = 0n;
  for (const c of suffix) {
    const v = DECODE[c];
    if (v === undefined) {
      throw new Error(
        `invalid typeid suffix '${suffix}': '${c}' is not lowercase Crockford base32`,
      );
    }
    n = (n << 5n) | BigInt(v);
  }
  const bytes = new Uint8Array(16);
  for (let i = 15; i >= 0; i--) {
    bytes[i] = Number(n & 0xffn);
    n >>= 8n;
  }
  return bytes;
}

function uuidString(bytes: Uint8Array): string {
  const hex = [...bytes].map((b) => b.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

function uuidBytes(uuid: string): Uint8Array {
  const hex = uuid.replace(/-/g, '');
  if (!/^[0-9a-fA-F]{32}$/.test(hex)) {
    throw new Error(`invalid uuid '${uuid}'`);
  }
  const bytes = new Uint8Array(16);
  for (let i = 0; i < 16; i++) bytes[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  return bytes;
}

// UUIDv7 (RFC 9562): 48-bit Unix-ms timestamp, version 7, variant 10,
// the remaining 74 bits random.
function uuidv7(): Uint8Array {
  const bytes = new Uint8Array(16);
  globalThis.crypto.getRandomValues(bytes);
  let ms = BigInt(Date.now());
  for (let i = 5; i >= 0; i--) {
    bytes[i] = Number(ms & 0xffn);
    ms >>= 8n;
  }
  bytes[6] = (bytes[6]! & 0x0f) | 0x70;
  bytes[8] = (bytes[8]! & 0x3f) | 0x80;
  return bytes;
}

function format(prefix: string, bytes: Uint8Array): string {
  const suffix = encode(bytes);
  return prefix === '' ? suffix : `${prefix}_${suffix}`;
}

/**
 * Canonical TypeID string for `prefix` and an existing UUID (any
 * version, hyphenated or not).
 *
 * @throws {Error} on an invalid prefix or UUID.
 */
export function fromUUID(prefix: string, uuid: string): string {
  checkPrefix(prefix);
  return format(prefix, uuidBytes(uuid));
}

/**
 * Compile-time-typed canonical TypeID string for prefix `P`.
 *
 * The template-literal `${P}_${string}` form means a `Typed<'task'>`
 * is statically distinguishable from a `Typed<'invoice'>` even though
 * both are runtime `string`s. Pair with {@link newTyped} or
 * {@link parseTyped} to mint values whose prefix is enforced both at
 * compile time and at runtime.
 */
export type Typed<P extends string> = `${P}_${string}`;

/** Parsed components of a canonical TypeID string. */
export interface Parsed {
  /** Prefix segment (everything before the final underscore). */
  prefix: string;
  /** Backing UUID in canonical 8-4-4-4-12 hyphenated form. */
  uuid: string;
}

/**
 * Generate a new TypeID with the given prefix, returning the canonical
 * string `<prefix>_<26-char-base32>`.
 *
 * @example
 *   newId('task') // => 'task_01h2e8kqv8000000000000000'
 */
export function newId(prefix: string): string {
  checkPrefix(prefix);
  return format(prefix, uuidv7());
}

/**
 * Parse a canonical TypeID string into its prefix and backing UUID.
 *
 * @throws {Error} when the input is not a syntactically valid TypeID
 *   (e.g. malformed suffix, illegal prefix characters).
 */
export function parse(s: string): Parsed {
  const sep = s.lastIndexOf('_');
  const prefix = sep === -1 ? '' : s.slice(0, sep);
  const suffix = sep === -1 ? s : s.slice(sep + 1);
  if (sep !== -1 && prefix === '') {
    throw new Error(`invalid typeid '${s}': empty prefix with a separator`);
  }
  checkPrefix(prefix);
  return { prefix, uuid: uuidString(decode(suffix)) };
}

/**
 * Typed variant of {@link newId}. Returns a `Typed<P>` whose static
 * type carries the literal prefix, enabling compile-time discrimination
 * between e.g. `TaskId` and `InvoiceId`.
 */
export function newTyped<P extends string>(prefix: P): Typed<P> {
  return newId(prefix) as Typed<P>;
}

/**
 * Typed variant of {@link parse}. Validates that the parsed prefix
 * matches the expected `prefix` argument at runtime; returns the
 * canonical string narrowed to `Typed<P>`.
 *
 * @throws {Error} when the parsed prefix does not match `prefix`, or
 *   when the input is not a syntactically valid TypeID.
 */
export function parseTyped<P extends string>(prefix: P, s: string): Typed<P> {
  const parsed = parse(s);
  if (parsed.prefix !== prefix) {
    throw new Error(`typeid '${s}' has prefix '${parsed.prefix}', want '${prefix}'`);
  }
  return s as Typed<P>;
}

// Crockford base32 alphabet used by the TypeID spec (lowercase),
// excluding i, l, o, u to avoid look-alike confusion. Module-internal:
// callers should validate via `typeIdSchema(prefix)` instead.
const SUFFIX_RE = /^[0-7][0-9a-hjkmnp-tv-z]{25}$/;

/**
 * Zod schema for runtime validation of a `${prefix}_<26-char-base32>`
 * canonical TypeID string.
 *
 * The schema only validates structure (prefix match + 26-char Crockford
 * base32 suffix with the spec's leading-bit constraint). It does not
 * decode the UUIDv7 timestamp; use {@link parse} if you need the
 * underlying UUID.
 *
 * @example
 *   const TaskIdSchema = typeIdSchema('task');
 *   TaskIdSchema.parse('task_01h2e8kqv8000000000000000');
 */
export function typeIdSchema(prefix: string): z.ZodString {
  const pattern = new RegExp(
    `^${escapeRegExp(prefix)}_${SUFFIX_RE.source.slice(1, -1)}$`,
  );
  return z
    .string()
    .regex(pattern, `invalid typeid for prefix '${prefix}'`);
}

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}
