/**
 * @packageDocumentation
 *
 * Version check and notify — compares the running tool version against the
 * latest GitHub release and writes a human-readable upgrade notice to any
 * {@link https://nodejs.org/api/stream.html#class-streamwritable | Writable}
 * stream when a newer release is available.
 *
 * ### Cache behaviour
 * Results are persisted to `{stateDir}/.upgrade-{name}-cache.json`.
 * The cache stores `{ version, checkedAt }`. On the next call within `cacheTTL`
 * milliseconds the remote API is not contacted; the cached version is used
 * directly. After the TTL expires the cache is refreshed on the next call.
 * Cache I/O errors are swallowed — a stale or missing cache simply causes a
 * fresh fetch.
 *
 * ### Error handling philosophy
 * Version checking is a best-effort, non-critical operation.  Any failure —
 * network error, bad JSON, HTTP error, filesystem error, invalid semver — is
 * silently discarded.  The function always resolves without throwing.  If
 * something goes wrong the user sees neither a notice nor an error.
 *
 * ### Notice format
 * ```
 * \nA new release of {name} is available: v{current} → v{latest}\nhttps://github.com/{owner}/{repo}/releases/latest\n
 * ```
 *
 * ### Usage example
 * ```ts
 * import { createChecker } from '@hop-top/kit/upgrade';
 *
 * const checker = createChecker({
 *   name: 'my-cli',
 *   currentVersion: '1.2.3',
 *   owner: 'myorg',
 *   repo: 'my-cli',
 * });
 *
 * // At the end of your CLI command:
 * await checker.notifyIfAvailable(process.stderr);
 * ```
 */

import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';
import semver from 'semver';

// ─── Public types ─────────────────────────────────────────────────────────────

/**
 * Static configuration for a version {@link Checker}.
 */
export interface CheckerOptions {
  /** Human-readable tool name shown in the upgrade notice. */
  name: string;
  /** Currently running version string (semver, without leading `v`). */
  currentVersion: string;
  /** GitHub organisation or username that owns the repository. */
  owner: string;
  /** GitHub repository name. */
  repo: string;
  /**
   * How long (ms) a cached result stays valid before a fresh fetch is made.
   * @defaultValue 86_400_000 (24 hours)
   */
  cacheTTL?: number;
  /**
   * Directory used to store the cache file.
   * @defaultValue `os.tmpdir()`
   */
  stateDir?: string;
  /**
   * HTTP request timeout in milliseconds.
   * @defaultValue 10_000
   */
  timeout?: number;
}

/**
 * Returned by {@link createChecker}.  Call {@link Checker.notifyIfAvailable}
 * at the end of a CLI command to surface upgrade notices non-intrusively.
 */
export interface Checker {
  /**
   * Checks for a newer GitHub release and writes a one-line notice to `out`
   * if one is available.  Silently swallows all errors.
   *
   * @param out - Any Node.js writable stream (e.g. `process.stderr`).
   */
  notifyIfAvailable(out: NodeJS.WritableStream): Promise<void>;
}

// ─── Internal types ───────────────────────────────────────────────────────────

interface CacheEntry {
  version: string;
  checkedAt: number;
}

// ─── Implementation ───────────────────────────────────────────────────────────

/**
 * Creates a {@link Checker} configured with the provided options.
 *
 * The checker is stateless beyond what is persisted in the cache file; it is
 * safe to create multiple instances with the same options.
 *
 * @param opts - Configuration options; see {@link CheckerOptions}.
 * @returns A {@link Checker} ready to call.
 */
export function createChecker(opts: CheckerOptions): Checker {
  const {
    name,
    currentVersion,
    owner,
    repo,
    cacheTTL = 24 * 60 * 60 * 1000,
    stateDir = os.tmpdir(),
    timeout = 10_000,
  } = opts;

  const cacheFile = path.join(stateDir, `.upgrade-${name}-cache.json`);
  const releaseURL = `https://api.github.com/repos/${owner}/${repo}/releases/latest`;
  const releasesPageURL = `https://github.com/${owner}/${repo}/releases/latest`;

  // ── cache helpers ──────────────────────────────────────────────────────────

  function readCache(): CacheEntry | null {
    try {
      const raw = fs.readFileSync(cacheFile, 'utf8');
      const parsed = JSON.parse(raw) as CacheEntry;
      if (typeof parsed.version === 'string' && typeof parsed.checkedAt === 'number') {
        return parsed;
      }
    } catch {
      // Missing or malformed cache — treat as miss.
    }
    return null;
  }

  function writeCache(version: string): void {
    try {
      const entry: CacheEntry = { version, checkedAt: Date.now() };
      fs.writeFileSync(cacheFile, JSON.stringify(entry), 'utf8');
    } catch {
      // Cache write failure is non-fatal.
    }
  }

  // ── fetch latest version ───────────────────────────────────────────────────

  async function fetchLatestVersion(): Promise<string | null> {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeout);
    try {
      const res = await fetch(releaseURL, {
        signal: controller.signal,
        headers: { Accept: 'application/vnd.github+json' },
      });
      if (!res.ok) return null;
      const body = (await res.json()) as { tag_name?: string };
      const tag = body.tag_name;
      if (typeof tag !== 'string') return null;
      // Strip leading 'v' so semver.gt works with or without prefix.
      return tag.replace(/^v/, '');
    } finally {
      clearTimeout(timer);
    }
  }

  // ── notifyIfAvailable ──────────────────────────────────────────────────────

  async function notifyIfAvailable(out: NodeJS.WritableStream): Promise<void> {
    try {
      let latestVersion: string | null = null;

      // Check cache first.
      const cached = readCache();
      if (cached !== null && Date.now() - cached.checkedAt < cacheTTL) {
        latestVersion = cached.version;
      } else {
        latestVersion = await fetchLatestVersion();
        if (latestVersion !== null) {
          writeCache(latestVersion);
        }
      }

      if (latestVersion === null) return;

      // Only notify when latest is strictly greater than current.
      if (!semver.gt(latestVersion, currentVersion)) return;

      const notice =
        `\nA new release of ${name} is available: v${currentVersion} → v${latestVersion}\n` +
        `${releasesPageURL}\n`;

      out.write(notice);
    } catch {
      // Any unexpected error is silently swallowed.
    }
  }

  return { notifyIfAvailable };
}
