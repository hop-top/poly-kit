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
 * silently discarded by `notifyIfAvailable`, which always resolves without
 * throwing.  `check()` is the strict variant: it rejects with an
 * {@link UpgradeCheckError} so callers (e.g. `upgrade --check`) can tell a
 * failed lookup from "up to date".
 *
 * ### Notice format
 * ```
 * \nA new release of {name} is available: v{current} → v{latest}\nhttps://github.com/{owner}/{repo}/releases/latest\n
 * ```
 *
 * ### Component tags and prereleases
 * By default the check reads `releases/latest` and strips a leading `v`.
 * Set `tagPrefix` (e.g. `xray/v`) for a monorepo whose tags carry a
 * component: releases are listed, tags without the prefix are ignored, and
 * the notice links to the matching release. Set `includePrereleases` to
 * consider prereleases; without it, prereleases are never offered. Both
 * options list at most 3 pages of 100 releases, newest first, drafts
 * skipped; the highest semver wins.
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
  /**
   * Tag prefix identifying this tool's releases, stripped before the semver
   * comparison (e.g. `'xray/v'` for tags like `xray/v0.2.0`). Tags without
   * the prefix are not candidates. Setting it lists releases instead of
   * reading `releases/latest`, which may belong to another component.
   * @defaultValue `''` (no prefix; `releases/latest`)
   */
  tagPrefix?: string;
  /**
   * Offer prereleases (e.g. `0.1.0-rc.1`) as upgrades. Setting it lists
   * releases instead of reading `releases/latest`, which skips prereleases.
   * @defaultValue false
   */
  includePrereleases?: boolean;
}

/** Outcome of {@link Checker.check}. */
export interface UpgradeCheckResult {
  /** Running version, as configured. */
  current: string;
  /** Latest release version (no leading `v` / tag prefix). */
  latest: string;
  /** True when `latest` is strictly greater than `current`. */
  updateAvailable: boolean;
  /** Release page to link to. */
  url: string;
}

/** Rejection of {@link Checker.check}; the underlying error is in `cause`. */
export class UpgradeCheckError extends Error {
  constructor(message: string, options?: { cause?: unknown }) {
    super(message, options);
    this.name = 'UpgradeCheckError';
  }
}

/**
 * Returned by {@link createChecker}.  Call {@link Checker.notifyIfAvailable}
 * at the end of a CLI command to surface upgrade notices non-intrusively.
 */
export interface Checker {
  /**
   * Looks up the latest release and resolves with the outcome. Unlike
   * {@link Checker.notifyIfAvailable} it never swallows failures: network,
   * HTTP, parse and "no matching release" errors reject with an
   * {@link UpgradeCheckError}, so callers can tell a failed lookup from
   * "up to date". Honours `tagPrefix`, `includePrereleases` and the cache;
   * pass `cacheTTL: 0` to bypass the cache.
   */
  check(): Promise<UpgradeCheckResult>;

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
  /** Tag of the cached release; written only when releases are listed. */
  tag?: string;
}

interface GitHubRelease {
  tag_name?: unknown;
  draft?: unknown;
  prerelease?: unknown;
}

/** Releases requested per page when listing (GitHub's maximum). */
const PAGE_SIZE = 100;
/** Pages fetched at most when listing: bounds the check to 3 requests. */
const MAX_PAGES = 3;

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
    tagPrefix = '',
    includePrereleases = false,
  } = opts;

  // Default mode (no prefix, no prereleases) keeps the original
  // `releases/latest` lookup untouched.
  const listMode = tagPrefix !== '' || includePrereleases;

  const cacheFile = path.join(stateDir, `.upgrade-${name}-cache.json`);
  const releaseURL = `https://api.github.com/repos/${owner}/${repo}/releases/latest`;
  const listURL = `https://api.github.com/repos/${owner}/${repo}/releases`;
  const releasesPageURL = `https://github.com/${owner}/${repo}/releases/latest`;

  // `releases/latest` can be another component's release or 404 while only
  // prereleases exist, so in list mode link the exact release, else the list.
  function noticeURL(tag: string | undefined): string {
    if (!listMode) return releasesPageURL;
    const base = `https://github.com/${owner}/${repo}/releases`;
    if (tag === undefined) return base;
    return `${base}/tag/${tag.split('/').map(encodeURIComponent).join('/')}`;
  }

  // ── cache helpers ──────────────────────────────────────────────────────────

  function tagNamesVersion(tag: unknown, version: string): boolean {
    return typeof tag === 'string' && tag.startsWith(tagPrefix) &&
      semver.valid(tag.slice(tagPrefix.length).replace(/^v/, '')) === version;
  }

  function readCache(): CacheEntry | null {
    try {
      const raw = fs.readFileSync(cacheFile, 'utf8');
      const parsed = JSON.parse(raw) as CacheEntry;
      if (typeof parsed.version === 'string' && typeof parsed.checkedAt === 'number') {
        // The tag becomes part of a link, and the cache file may sit in a
        // shared directory: trust it only if it names this cached version.
        if (listMode && !tagNamesVersion(parsed.tag, parsed.version)) {
          return null;
        }
        return parsed;
      }
    } catch {
      // Missing or malformed cache — treat as miss.
    }
    return null;
  }

  function writeCache(version: string, tag?: string): void {
    try {
      const entry: CacheEntry = { version, checkedAt: Date.now() };
      if (tag !== undefined) entry.tag = tag;
      fs.writeFileSync(cacheFile, JSON.stringify(entry), 'utf8');
    } catch {
      // Cache write failure is non-fatal.
    }
  }

  // ── fetch latest version ───────────────────────────────────────────────────

  interface Latest {
    version: string;
    tag?: string;
  }

  async function fetchLatestVersion(): Promise<Latest> {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), timeout);
    try {
      if (listMode) return await fetchHighestRelease(controller.signal);
      const res = await fetch(releaseURL, {
        signal: controller.signal,
        headers: { Accept: 'application/vnd.github+json' },
      });
      if (!res.ok) throw new UpgradeCheckError(`release lookup failed: HTTP ${res.status}`);
      const body = (await res.json()) as { tag_name?: string };
      const tag = body.tag_name;
      if (typeof tag !== 'string') throw new UpgradeCheckError('release lookup failed: no tag_name');
      // Strip leading 'v' so semver.gt works with or without prefix.
      return { version: tag.replace(/^v/, '') };
    } finally {
      clearTimeout(timer);
    }
  }

  /**
   * Lists releases (newest first, bounded) and returns the highest semver
   * among non-draft releases whose tag starts with `tagPrefix`; prereleases
   * only when `includePrereleases` is set.
   */
  async function fetchHighestRelease(signal: AbortSignal): Promise<Latest> {
    let best: Latest | null = null;
    for (let page = 1; page <= MAX_PAGES; page++) {
      const res = await fetch(`${listURL}?per_page=${PAGE_SIZE}&page=${page}`, {
        signal,
        headers: { Accept: 'application/vnd.github+json' },
      });
      if (!res.ok) {
        // Page 1 failing is a failed check; a later page keeps what we have.
        if (page === 1) throw new UpgradeCheckError(`release lookup failed: HTTP ${res.status}`);
        break;
      }
      const body = (await res.json()) as unknown;
      if (!Array.isArray(body)) {
        if (page === 1) throw new UpgradeCheckError('release lookup failed: unexpected response');
        break;
      }
      for (const rel of body as GitHubRelease[]) {
        if (rel === null || typeof rel !== 'object') continue;
        if (rel.draft === true || typeof rel.tag_name !== 'string') continue;
        const tag = rel.tag_name;
        if (!tag.startsWith(tagPrefix)) continue;
        const version = semver.valid(tag.slice(tagPrefix.length).replace(/^v/, ''));
        if (version === null) continue;
        if (!includePrereleases && (rel.prerelease === true || semver.prerelease(version) !== null)) {
          continue;
        }
        if (best === null || semver.gt(version, best.version)) best = { version, tag };
      }
      if (body.length < PAGE_SIZE) break;
    }
    if (best === null) throw new UpgradeCheckError('no matching release found');
    return best;
  }

  // ── check ──────────────────────────────────────────────────────────────────

  async function check(): Promise<UpgradeCheckResult> {
    try {
      let latestVersion: string;
      let latestTag: string | undefined;

      const cached = readCache();
      if (cached !== null && Date.now() - cached.checkedAt < cacheTTL) {
        latestVersion = cached.version;
        latestTag = cached.tag;
      } else {
        const latest = await fetchLatestVersion();
        latestVersion = latest.version;
        latestTag = latest.tag;
        writeCache(latest.version, latest.tag);
      }

      return {
        current: currentVersion,
        latest: latestVersion,
        // Only strictly greater counts as an update.
        updateAvailable: semver.gt(latestVersion, currentVersion),
        url: noticeURL(latestTag),
      };
    } catch (err) {
      if (err instanceof UpgradeCheckError) throw err;
      throw new UpgradeCheckError(
        `upgrade check failed: ${err instanceof Error ? err.message : String(err)}`,
        { cause: err },
      );
    }
  }

  // ── notifyIfAvailable ──────────────────────────────────────────────────────

  async function notifyIfAvailable(out: NodeJS.WritableStream): Promise<void> {
    try {
      const r = await check();
      if (!r.updateAvailable) return;
      out.write(
        `\nA new release of ${name} is available: v${r.current} → v${r.latest}\n` +
          `${r.url}\n`,
      );
    } catch {
      // Best-effort: any failure is silently swallowed.
    }
  }

  return { check, notifyIfAvailable };
}
