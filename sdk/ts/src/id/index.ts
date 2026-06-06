/**
 * @module id
 * @package @hop-top/kit
 *
 * TypeID primitive — thin wrapper around `typeid-js` (jetify-com)
 * implementing the cross-language kit API SHAPE per ADR 0001.
 *
 * Canonical wire form: `<prefix>_<26-char-base32>` where the suffix is
 * a Crockford-base32-encoded UUIDv7. The TS surface adds a
 * template-literal `Typed<P>` for compile-time prefix safety on top of
 * the runtime guarantees provided by `typeid-js`.
 *
 * URI composition (e.g. `tlc://task/task_…`) is NOT in this module —
 * callers should use the `@hop-top/cite` package directly. This
 * keeps `kit/id` free of any transitive dependency on the URI
 * registry, per the ADR's scope boundary.
 */

import { typeid, TypeID } from 'typeid-js';
import { z } from 'zod';

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
  return typeid(prefix).toString();
}

/**
 * Parse a canonical TypeID string into its prefix and backing UUID.
 *
 * @throws {Error} when the input is not a syntactically valid TypeID
 *   (e.g. malformed suffix, illegal prefix characters).
 */
export function parse(s: string): Parsed {
  const tid = TypeID.fromString(s);
  return {
    prefix: tid.getType(),
    uuid: tid.toUUID(),
  };
}

/**
 * Typed variant of {@link newId}. Returns a `Typed<P>` whose static
 * type carries the literal prefix, enabling compile-time discrimination
 * between e.g. `TaskId` and `InvoiceId`.
 */
export function newTyped<P extends string>(prefix: P): Typed<P> {
  return typeid(prefix).toString() as Typed<P>;
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
  // TypeID.fromString with a prefix argument enforces the match and
  // throws on mismatch, giving us a single source of truth for both
  // structural and prefix validation.
  const tid = TypeID.fromString(s, prefix);
  return tid.toString() as Typed<P>;
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
