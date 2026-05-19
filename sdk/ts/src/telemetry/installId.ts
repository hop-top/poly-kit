/**
 * @module telemetry/installId
 * @package @hop-top/kit
 *
 * Persisted anonymous installation identifier — the TS mirror of
 * `go/runtime/telemetry/installid.go`.
 *
 * On-disk format: 32 raw bytes from `crypto.randomBytes` stored at
 * `<XDG_STATE_HOME>/kit/telemetry/installation_id`. The surface API
 * returns the lowercase hex SHA-256 of those bytes (64 chars) so the
 * file format stays canonical across polyglot SDKs (Go / Py / TS / Rs /
 * PHP) while the hashed string is what flows through events.
 *
 * Perms: file 0600, parent dir 0700.
 *
 * See ADR-0035 decision #4 for the spec.
 */

import { promises as fs } from 'node:fs';
import { existsSync } from 'node:fs';
import { randomBytes, createHash } from 'node:crypto';
import { homedir } from 'node:os';
import * as path from 'node:path';

/** installIDSize is the on-disk byte length. SHA-256 of 32 bytes → 64-char hex. */
const installIDSize = 32;
/** File mode required by ADR-0035 §4. */
const installIDFilePerm = 0o600;
/** Parent dir mode required by ADR-0035 §4. */
const installIDDirPerm = 0o700;

/**
 * xdgStateHome returns `$XDG_STATE_HOME` or `~/.local/state` per the
 * XDG Base Directory Spec.
 */
function xdgStateHome(): string {
  return process.env.XDG_STATE_HOME ?? path.join(homedir(), '.local', 'state');
}

/**
 * installIdPath returns the canonical on-disk path used by this module.
 * Useful for SDK consumers and compliance checks that need to verify
 * the storage location without invoking the read path.
 */
export function installIdPath(): string {
  return path.join(xdgStateHome(), 'kit', 'telemetry', 'installation_id');
}

/** sha256Hex returns the lowercase hex SHA-256 of buf. */
function sha256Hex(buf: Buffer): string {
  return createHash('sha256').update(buf).digest('hex');
}

/** readAndValidate reads the file and validates its byte length. */
async function readAndValidate(p: string): Promise<Buffer> {
  const data = await fs.readFile(p);
  if (data.length !== installIDSize) {
    throw new Error(
      `install_id: file ${p} has wrong size ${data.length} bytes, expected ${installIDSize}`,
    );
  }
  return data;
}

/**
 * getInstallId returns the persisted anonymous install identifier as
 * lowercase hex SHA-256 of 32 random bytes stored on disk. It generates
 * and persists the bytes on first call. Concurrent first calls across
 * processes are race-safe via a tmp + rename pattern with an
 * existence-precheck: if a peer already wrote a valid file by the time
 * we go to rename, we adopt their bytes.
 *
 * Mirrors `telemetry.InstallationID` in Go.
 */
export async function getInstallId(): Promise<string> {
  const p = installIdPath();

  // Fast path: file already exists.
  if (existsSync(p)) {
    const data = await readAndValidate(p);
    return sha256Hex(data);
  }

  // Slow path: generate + write atomically.
  const parent = path.dirname(p);
  await fs.mkdir(parent, { recursive: true, mode: installIDDirPerm });
  // Re-enforce 0700 in case the dir already existed at a looser mode.
  try {
    await fs.chmod(parent, installIDDirPerm);
  } catch {
    // Non-fatal: a parent we can't chmod (e.g. shared dir) is the
    // operator's problem, not ours.
  }

  const fresh = randomBytes(installIDSize);
  const tmp = p + '.new';
  await fs.writeFile(tmp, fresh, { mode: installIDFilePerm });

  // First writer wins. If a peer raced us and a valid file already
  // sits at `p`, drop our tmp and adopt their bytes.
  if (existsSync(p)) {
    try {
      await fs.unlink(tmp);
    } catch {
      // tmp may have already been swept; ignore.
    }
    const data = await readAndValidate(p);
    return sha256Hex(data);
  }

  await fs.rename(tmp, p);
  return sha256Hex(fresh);
}

/**
 * rotate atomically replaces the persisted bytes with 32 fresh
 * `crypto.randomBytes` and returns the new hex. Used by the
 * `kit consent reset` CLI flow.
 *
 * Mirrors `telemetry.Rotate` in Go.
 */
export async function rotate(): Promise<string> {
  const p = installIdPath();
  const parent = path.dirname(p);
  await fs.mkdir(parent, { recursive: true, mode: installIDDirPerm });
  try {
    await fs.chmod(parent, installIDDirPerm);
  } catch {
    // See getInstallId — non-fatal.
  }

  const fresh = randomBytes(installIDSize);
  const tmp = p + '.new';
  await fs.writeFile(tmp, fresh, { mode: installIDFilePerm });
  await fs.rename(tmp, p);
  return sha256Hex(fresh);
}

/**
 * resetForTest deletes the persisted file. Test helper exposed so
 * adopters can drive first-run code paths in their own tests. Never
 * call from production code.
 *
 * Mirrors `telemetry.ResetForTest` in Go.
 */
export async function resetForTest(): Promise<void> {
  const p = installIdPath();
  try {
    await fs.unlink(p);
  } catch {
    // Missing-file is fine.
  }
  // Also clean up any stale *.new from a crashed writer.
  try {
    await fs.unlink(p + '.new');
  } catch {
    // Missing-file is fine.
  }
}
