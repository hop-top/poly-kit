<?php

declare(strict_types=1);

namespace HopTop\Kit\Telemetry;

use RuntimeException;

/**
 * Read / write the persistent installation_id used to attribute
 * pseudonymous telemetry events.
 *
 * Wire format mirrors the Go ground truth in
 * go/runtime/telemetry/installid.go: 32 raw bytes on disk, returned to
 * callers as the lowercase SHA-256 hex digest of those bytes. Storing
 * the raw bytes (not the hash) lets us salt or rotate downstream
 * without touching the file format.
 */
final class InstallId
{
    /**
     * Canonical on-disk path:
     *   $XDG_STATE_HOME/kit/telemetry/installation_id
     * (defaults to $HOME/.local/state).
     */
    public static function path(): string
    {
        $stateHome = getenv('XDG_STATE_HOME');
        if ($stateHome === false || $stateHome === '') {
            $home = (string) ($_SERVER['HOME'] ?? getenv('HOME') ?: '');
            $stateHome = $home . '/.local/state';
        }

        return $stateHome . '/kit/telemetry/installation_id';
    }

    /**
     * Returns the SHA-256 hex digest of the 32 raw bytes persisted at
     * the canonical path. Generates + persists on first call.
     *
     * Race-safe across concurrent first calls: a losing writer accepts
     * the bytes written by the winner.
     *
     * @throws RuntimeException If the on-disk file has the wrong size
     *         or rename to the canonical path fails irrecoverably.
     */
    public static function get(): string
    {
        $p = self::path();

        if (file_exists($p)) {
            return self::hashOrThrow($p);
        }

        $parent = dirname($p);
        if (!is_dir($parent) && !@mkdir($parent, 0o700, true) && !is_dir($parent)) {
            throw new RuntimeException("install_id: failed to mkdir {$parent}");
        }

        $fresh = random_bytes(32);
        $tmp = $p . '.new';
        if (file_put_contents($tmp, $fresh) !== 32) {
            throw new RuntimeException("install_id: failed to write tmp file {$tmp}");
        }
        @chmod($tmp, 0o600);

        // Atomic rename; if another process won the race, accept their bytes.
        if (!@rename($tmp, $p)) {
            if (file_exists($p)) {
                @unlink($tmp);

                return self::hashOrThrow($p);
            }
            @unlink($tmp);
            throw new RuntimeException("install_id: rename failed for {$p}");
        }

        return hash('sha256', $fresh);
    }

    /**
     * Rotate the installation_id: overwrite with fresh bytes and
     * return the new SHA-256 hex digest. Used by the consent flow
     * when the user revokes + re-grants.
     *
     * @throws RuntimeException If the rename to the canonical path
     *         fails.
     */
    public static function rotate(): string
    {
        $p = self::path();
        $parent = dirname($p);
        if (!is_dir($parent) && !@mkdir($parent, 0o700, true) && !is_dir($parent)) {
            throw new RuntimeException("install_id: failed to mkdir {$parent}");
        }

        $fresh = random_bytes(32);
        $tmp = $p . '.new';
        if (file_put_contents($tmp, $fresh) !== 32) {
            throw new RuntimeException("install_id: failed to write tmp file {$tmp}");
        }
        @chmod($tmp, 0o600);

        if (!@rename($tmp, $p)) {
            @unlink($tmp);
            throw new RuntimeException("install_id: rotate rename failed for {$p}");
        }

        return hash('sha256', $fresh);
    }

    /**
     * Test helper: remove the canonical file so the next get() call
     * regenerates. No-op when absent.
     */
    public static function resetForTest(): void
    {
        $p = self::path();
        if (file_exists($p)) {
            @unlink($p);
        }
    }

    /**
     * @throws RuntimeException If the file is the wrong size.
     */
    private static function hashOrThrow(string $p): string
    {
        $data = file_get_contents($p);
        if ($data === false) {
            throw new RuntimeException("install_id: failed to read {$p}");
        }
        $len = strlen($data);
        if ($len !== 32) {
            throw new RuntimeException(
                "install_id: file has wrong size {$len} bytes, expected 32"
            );
        }

        return hash('sha256', $data);
    }
}
