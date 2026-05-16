/**
 * @module safety
 * @package @hop-top/kit
 *
 * Factor 10 — Delegation Safety.
 *
 * Guards destructive operations behind --force, with stricter
 * requirements in non-TTY (scripted/agent) contexts.
 */

export type SafetyLevel = 'read' | 'caution' | 'dangerous';

/**
 * Validates that the current context permits the requested safety level.
 *
 * - `read`: always passes
 * - `caution`: passes in TTY; requires `--force` in non-TTY
 * - `dangerous`: always requires `--force`
 *
 * @throws {Error} when guard fails, with message referencing --force
 */
export function safetyGuard(
  level: SafetyLevel,
  opts?: { force?: boolean },
): void {
  if (level === 'read') return;

  const force = opts?.force ?? false;
  const isTTY = !!process.stdout.isTTY;

  if (level === 'dangerous' && !force) {
    throw new Error(
      'dangerous operation requires --force',
    );
  }

  if (level === 'caution' && !isTTY && !force) {
    throw new Error(
      'caution-level operation in non-TTY requires --force',
    );
  }
}
