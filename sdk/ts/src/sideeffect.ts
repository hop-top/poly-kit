/**
 * @module sideeffect
 * @package @hop-top/kit
 *
 * The `kit/side-effect` vocabulary and the one name-inference table every
 * kit gate and projection reads. Mirrors Go's `cli.SideEffect` tiers and
 * `cmdmeta.InferredDestructive`.
 */

/** Annotation key a command declares its side-effect tier under. */
export const SIDE_EFFECT_ANNOTATION = 'kit/side-effect';

/** The closed set of declarable tiers (legacy 4-tier plus the 6-tier ladder). */
export const SIDE_EFFECT_TIERS = [
  'read',
  'write',
  'write-local',
  'write-shared',
  'destructive',
  'destructive-local',
  'destructive-shared',
  'interactive',
] as const;

export type SideEffect = (typeof SIDE_EFFECT_TIERS)[number];

/** The tier an unannotated command with a destructive leaf name is gated as. */
export const INFERRED_TIER: SideEffect = 'destructive-shared';

const WRITE_LIKE = new Set<string>(['write', 'write-local', 'write-shared']);
const DESTRUCTIVE_LIKE = new Set<string>([
  'destructive',
  'destructive-local',
  'destructive-shared',
]);

/** True for any tier that mutates state without irreversible loss. */
export function isWriteLike(tier: string): boolean {
  return WRITE_LIKE.has(tier);
}

/** True for any tier that performs an irreversible mutation. */
export function isDestructiveLike(tier: string): boolean {
  return DESTRUCTIVE_LIKE.has(tier);
}

/** True when `tier` names a declarable side-effect tier. */
export function isValidSideEffect(tier: string): boolean {
  return (SIDE_EFFECT_TIERS as readonly string[]).includes(tier);
}

/**
 * The one table of leaf names kit treats as destructive when a command
 * carries no `kit/side-effect` annotation. Every gate and projection in
 * this SDK reads it through {@link isDestructiveName} or
 * {@link inferredDestructive}; none keeps its own copy.
 */
const DESTRUCTIVE_NAMES: ReadonlySet<string> = new Set([
  'delete',
  'remove',
  'rm',
  'destroy',
  'purge',
  'drop',
]);

/** True when `name` is in the destructive-name table. */
export function isDestructiveName(name: string): boolean {
  return DESTRUCTIVE_NAMES.has(name);
}

/** The table's names, sorted. */
export function destructiveNames(): string[] {
  return [...DESTRUCTIVE_NAMES].sort();
}

/**
 * True when a command is destructive by name alone: its annotation is
 * absent or empty and its leaf name is in the table. An explicit
 * annotation, valid or not, always wins: a malformed value is a
 * declaration defect, never inferred over.
 */
export function inferredDestructive(
  name: string,
  annotations: Readonly<Record<string, string>> | undefined,
): boolean {
  const declared = annotations?.[SIDE_EFFECT_ANNOTATION];
  if (declared !== undefined && declared !== '') return false;
  return isDestructiveName(name);
}

/**
 * The tier the delegation gates act on: the declared `kit/side-effect`,
 * else {@link INFERRED_TIER} for a destructive-named command. Undefined
 * when neither says anything. A declared value, valid or malformed, is
 * never replaced.
 */
export function gateSideEffect(
  name: string,
  annotations: Readonly<Record<string, string>> | undefined,
): string | undefined {
  if (inferredDestructive(name, annotations)) return INFERRED_TIER;
  const declared = annotations?.[SIDE_EFFECT_ANNOTATION];
  return declared === undefined ? undefined : declared;
}
