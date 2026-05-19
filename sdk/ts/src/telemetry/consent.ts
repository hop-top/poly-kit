/**
 * @module telemetry/consent
 * @package @hop-top/kit
 *
 * Read-only consent-file loader — the TS mirror of the read path in
 * `go/core/consent/file_store.go`.
 *
 * On-disk shape (YAML under `<XDG_CONFIG_HOME>/kit/config.yaml` at the
 * `kit.telemetry.consent` partition):
 *
 * ```yaml
 * kit:
 *   telemetry:
 *     consent:
 *       state: granted          # | denied
 *       prompt_version: 1
 *       decision_source: prompt # | flag | config | env
 *       decided_at: 2026-05-19T12:00:00Z
 * ```
 *
 * A pre-refactor layout at `<XDG_CONFIG_HOME>/kit/telemetry.yaml` (with
 * a bare `telemetry.consent` block at the top level) is read as a
 * fallback so installs that have not been migrated yet still work.
 *
 * This TS module is **read-only**: writing the consent file remains the
 * job of the Go `kit consent` CLI. SDK consumers just need to know
 * whether emission is allowed.
 *
 * Anything that isn't a clean, parseable, `state: granted` block
 * collapses to `deniedConsent` — a fail-closed default so a corrupt
 * config can never accidentally enable emission.
 */

import { promises as fs } from 'node:fs';
import { existsSync } from 'node:fs';
import { homedir } from 'node:os';
import * as path from 'node:path';
import * as yaml from 'js-yaml';

/**
 * Consent is the resolved telemetry decision for the current
 * installation. The shape mirrors the relevant subset of the Go
 * `consent.Decision` struct.
 */
export interface Consent {
  /** allowed is true only when state == "granted". */
  readonly allowed: boolean;
  /** promptVersion records which prompt revision the user accepted. */
  readonly promptVersion: number;
  /**
   * decisionSource records how the decision was captured —
   * "prompt" | "flag" | "config" | "env". Defaults to "config" when
   * the file is missing or unreadable.
   */
  readonly decisionSource: string;
  /** decidedAt is the RFC3339 timestamp of the decision, if present. */
  readonly decidedAt?: string;
}

/**
 * deniedConsent is the canonical fail-closed default. Returned whenever
 * the file is missing, unreadable, malformed, or carries an unknown
 * state value.
 */
export const deniedConsent: Consent = {
  allowed: false,
  promptVersion: 0,
  decisionSource: 'config',
};

/** xdgConfigHome returns `$XDG_CONFIG_HOME` or `~/.config`. */
function xdgConfigHome(): string {
  return process.env.XDG_CONFIG_HOME ?? path.join(homedir(), '.config');
}

/**
 * consentPath returns the canonical on-disk path used by this module
 * (`<XDG_CONFIG_HOME>/kit/config.yaml`). Useful for `kit telemetry
 * status` diagnostics and SDK consumers that want to surface the
 * location.
 */
export function consentPath(): string {
  return path.join(xdgConfigHome(), 'kit', 'config.yaml');
}

/**
 * legacyConsentPath returns the pre-refactor consent file location
 * (`<XDG_CONFIG_HOME>/kit/telemetry.yaml`). Read-only fallback consumed
 * by `loadConsent`; SDK callers should prefer `consentPath`.
 */
export function legacyConsentPath(): string {
  return path.join(xdgConfigHome(), 'kit', 'telemetry.yaml');
}

/**
 * loadConsent reads and parses the consent file, returning a fully
 * resolved Consent struct. Failure modes fold to `deniedConsent`:
 *
 *   - File missing → denied.
 *   - File unreadable → denied.
 *   - YAML parse error → denied.
 *   - Missing `kit.telemetry.consent` block (and no legacy) → denied.
 *   - `state` neither "granted" nor "denied" → denied.
 *
 * `state: "denied"` returns a Consent with `allowed=false` but with the
 * other fields populated, so callers can distinguish an explicit deny
 * from a fail-closed default.
 *
 * Read order: canonical `config.yaml` (`kit.telemetry.consent`) first,
 * then legacy `telemetry.yaml` (`telemetry.consent`) as a fallback.
 */
export async function loadConsent(): Promise<Consent> {
  const canonical = await readConsentBlock(consentPath(), 'canonical');
  if (canonical !== null) {
    return canonical;
  }
  const legacy = await readConsentBlock(legacyConsentPath(), 'legacy');
  if (legacy !== null) {
    return legacy;
  }
  return deniedConsent;
}

/**
 * readConsentBlock loads one YAML file and tries to decode a consent
 * block from it. Returns the resolved Consent, or null when the file
 * is missing / unreadable / malformed / lacks the expected block.
 * Distinguishing null from `deniedConsent` lets the caller fall
 * through to the next candidate path.
 */
async function readConsentBlock(
  p: string,
  variant: 'canonical' | 'legacy',
): Promise<Consent | null> {
  if (!existsSync(p)) {
    return null;
  }

  let data: unknown;
  try {
    const raw = await fs.readFile(p, 'utf8');
    data = yaml.load(raw);
  } catch {
    return null;
  }

  if (typeof data !== 'object' || data === null) {
    return null;
  }
  const root = data as Record<string, unknown>;

  // Canonical: kit.telemetry.consent. Legacy: telemetry.consent (no kit).
  let telemetry: unknown;
  if (variant === 'canonical') {
    const kit = root.kit;
    if (typeof kit !== 'object' || kit === null) {
      return null;
    }
    telemetry = (kit as Record<string, unknown>).telemetry;
  } else {
    telemetry = root.telemetry;
  }

  if (typeof telemetry !== 'object' || telemetry === null) {
    return null;
  }
  const block = (telemetry as Record<string, unknown>).consent;
  if (typeof block !== 'object' || block === null) {
    return null;
  }
  const c = block as Record<string, unknown>;

  const state = c.state;
  if (state !== 'granted' && state !== 'denied') {
    return null;
  }

  return {
    allowed: state === 'granted',
    promptVersion: typeof c.prompt_version === 'number' ? c.prompt_version : 0,
    decisionSource:
      typeof c.decision_source === 'string' ? c.decision_source : 'config',
    decidedAt: parseDecidedAt(c.decided_at),
  };
}

/**
 * parseDecidedAt coerces the raw YAML value into an RFC3339 string.
 * js-yaml's default schema promotes unquoted ISO timestamps to JS
 * `Date` objects, so we accept either form. Go writes `time.Time` as
 * an RFC3339 string with the YAML `!!timestamp` tag — both reader
 * paths converge here.
 */
function parseDecidedAt(v: unknown): string | undefined {
  if (typeof v === 'string') {
    return v;
  }
  if (v instanceof Date && !Number.isNaN(v.getTime())) {
    return v.toISOString().replace(/\.\d{3}Z$/, 'Z');
  }
  return undefined;
}
