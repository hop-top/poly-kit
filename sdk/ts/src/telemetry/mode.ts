/**
 * @module telemetry/mode
 * @package @hop-top/kit
 *
 * Telemetry Mode + env-variable precedence — the TS mirror of
 * `go/runtime/telemetry/mode.go`. Three tiers control what an emit
 * call ships:
 *
 *   - `Mode.Off`:  default; emit is a no-op.
 *   - `Mode.Anon`: anonymous payload (installation_id, command_path,
 *     exit_code, duration_ms, occurred_at, kit_version, sdk_lang/version).
 *   - `Mode.Full`: anon + args + flags, both post-redact.
 *
 * See ADR-0035 / ADR-0038 for the decision record and the canonical
 * event-schema doc at `sdk/docs/telemetry-event-schema.md` for the
 * on-wire shape.
 *
 * The env-precedence rule is intentionally simple:
 *
 *   1. If `KIT_APP_PREFIX=spaced` is set AND `SPACED_TELEMETRY_MODE` has
 *      a parseable value, that wins.
 *   2. Otherwise `KIT_TELEMETRY_MODE` is consulted.
 *   3. Anything else (missing, garbage, whitespace) → `Mode.Off`.
 */

/**
 * Mode controls how much detail the telemetry emitter ships.
 *
 * Encoded as a string-literal union so the on-disk / on-wire token IS
 * the enum value. Mirrors Go's `Mode.String()` which renders these same
 * three lowercase tokens.
 */
export const Mode = {
  Off: 'off',
  Anon: 'anon',
  Full: 'full',
} as const;

export type Mode = (typeof Mode)[keyof typeof Mode];

/**
 * parseMode maps a string token (case-insensitive: "off", "anon",
 * "full") to a Mode. Empty / null / undefined input returns
 * `[Mode.Off, true]` so an unset env var doesn't masquerade as an
 * error. Unknown tokens return `[Mode.Off, false]` so callers can
 * distinguish "operator typoed the env var" from "operator opted out".
 *
 * Mirrors `telemetry.ParseMode` in `go/runtime/telemetry/mode.go`.
 */
export function parseMode(s: string | undefined | null): [Mode, boolean] {
  if (s === undefined || s === null || s === '') {
    return [Mode.Off, true];
  }
  const low = s.trim().toLowerCase();
  if (low === '') {
    return [Mode.Off, true];
  }
  for (const m of Object.values(Mode)) {
    if (m === low) {
      return [m, true];
    }
  }
  return [Mode.Off, false];
}

/** resolveAppPrefix normalises KIT_APP_PREFIX to uppercase (matches Go). */
function resolveAppPrefix(env: NodeJS.ProcessEnv): string {
  return (env.KIT_APP_PREFIX ?? '').trim().toUpperCase();
}

/**
 * resolveMode reads the env vars in canonical precedence order and
 * returns the resolved Mode. Defaults to `Mode.Off`.
 *
 * Precedence (mirrors `telemetry.readEnvMode`):
 *
 *   1. `<APP>_TELEMETRY_MODE` where `<APP>` = uppercased `KIT_APP_PREFIX`.
 *   2. `KIT_TELEMETRY_MODE`.
 *   3. `Mode.Off`.
 *
 * Unparseable values at any tier are skipped (NOT a hard error); the
 * resolver falls through to the next tier and ultimately to Off.
 *
 * @param env Inject a custom env map for testing. Defaults to `process.env`.
 */
export function resolveMode(env: NodeJS.ProcessEnv = process.env): Mode {
  const app = resolveAppPrefix(env);
  if (app) {
    const v = (env[`${app}_TELEMETRY_MODE`] ?? '').trim();
    if (v) {
      const [m, ok] = parseMode(v);
      if (ok) {
        return m;
      }
    }
  }
  const v = (env.KIT_TELEMETRY_MODE ?? '').trim();
  if (v) {
    const [m, ok] = parseMode(v);
    if (ok) {
      return m;
    }
  }
  return Mode.Off;
}
