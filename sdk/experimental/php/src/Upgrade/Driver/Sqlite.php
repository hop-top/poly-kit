<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade\Driver;

use Closure;
use HopTop\Kit\Serve\Cancellation;
use HopTop\Kit\Upgrade\BackupUnsupported;
use HopTop\Kit\Upgrade\CanceledException;
use HopTop\Kit\Upgrade\FsUtil;
use HopTop\Kit\Upgrade\Locker;
use HopTop\Kit\Upgrade\SchemaDriver;
use HopTop\Kit\Upgrade\UpgradeException;
use HopTop\Kit\Upgrade\VersionFile;
use PDO;
use PDOException;

/**
 * Schema driver for a SQLite database file over `pdo_sqlite`. The migrator
 * keeps the version file; `version()` reads it back (see `ConfigFile`).
 *
 * Two modes, as in the Go driver. Path only (`$db` null): `backup` opens a
 * short-lived connection of its own and `restore` swaps the file. Handle
 * (`$db` given): `backup` runs on the caller's PDO, which is never closed
 * and whose attributes are never changed; `restore` throws
 * `BackupUnsupported` (the file cannot be swapped under a live handle).
 *
 * `backup` is `VACUUM INTO` (it reads through the WAL) written to
 * `<base>.tmp` and renamed to `<dest>/<db file name>`. `lock` is
 * `flock(LOCK_EX)` on `<data dir>/<name>/.lock`, the file the Go driver
 * locks, so both exclude each other.
 */
final class Sqlite implements SchemaDriver, Locker
{
    /** The files SQLite keeps beside a database. */
    private const SIDECARS = ['-wal', '-shm', '-journal'];

    private const LOCK_POLL_US = 50_000;

    /** @param string $dbPath the database file; with `$db` it only names the backup file (may be empty) */
    public function __construct(
        private readonly string $dbPath,
        private readonly string $name = 'sqlite',
        private readonly string $tool = '',
        private readonly string $dataDir = '',
        private readonly ?PDO $db = null,
    ) {}

    public function name(): string
    {
        return $this->name;
    }

    public function version(): string
    {
        return $this->dataDir !== ''
            ? VersionFile::readIn($this->dataDir, $this->name)
            : VersionFile::read($this->tool, $this->name);
    }

    public function setVersion(string $version): void {}

    /**
     * Writes a consistent snapshot to `<dest>/<db file name>`. Path only: a
     * missing database file is "nothing to back up" (no file, no error).
     * Needs no open transaction on the connection that runs it.
     */
    public function backup(string $dest): void
    {
        if ($this->db === null && !file_exists($this->dbPath)) {
            return;
        }
        try {
            FsUtil::mkdir($dest);
        } catch (UpgradeException $e) {
            throw new UpgradeException("create backup dir: {$e->getMessage()}", 0, $e);
        }
        $target = "$dest/" . $this->backupName();
        $tmp = "$target.tmp";
        foreach ([$tmp, $target] as $p) { // VACUUM INTO refuses an existing file
            if (file_exists($p) && !@unlink($p)) {
                throw new UpgradeException('clear backup file: ' . FsUtil::lastError());
            }
        }
        try {
            $this->vacuumInto($tmp);
            if (!@rename($tmp, $target)) {
                throw new UpgradeException('rename: ' . FsUtil::lastError());
            }
        } catch (\Throwable $e) {
            @unlink($tmp);
            throw new UpgradeException("backup db: {$e->getMessage()}", 0, $e);
        }
    }

    /**
     * Replaces the database file with the backup in `$src`, then removes the
     * replaced database's `-wal`, `-shm` and `-journal` files. Precondition:
     * no open handle on the file, in this process or another.
     *
     * @throws BackupUnsupported in handle mode; nothing is touched
     */
    public function restore(string $src): void
    {
        if ($this->db !== null) {
            throw new BackupUnsupported("restore {$this->dbPath} under a caller-owned handle: upgrade: backup unsupported");
        }
        $backup = "$src/" . $this->backupName();
        $in = @fopen($backup, 'rb');
        if ($in === false) {
            throw new UpgradeException('open backup: ' . FsUtil::lastError());
        }
        try {
            $dir = dirname($this->dbPath);
            try {
                FsUtil::mkdir($dir);
            } catch (UpgradeException $e) {
                throw new UpgradeException("create db dir: {$e->getMessage()}", 0, $e);
            }
            $mode = file_exists($this->dbPath) ? (int) (@fileperms($this->dbPath) & 0777) : 0600;
            $tmp = @tempnam($dir, basename($this->dbPath) . '.restore-');
            if ($tmp === false) {
                throw new UpgradeException('create db: ' . FsUtil::lastError());
            }
            try {
                self::fill($tmp, $in, $mode);
                if (!@rename($tmp, $this->dbPath)) {
                    throw new UpgradeException('restore db: ' . FsUtil::lastError());
                }
            } catch (\Throwable $e) {
                @unlink($tmp);
                throw $e instanceof UpgradeException ? new UpgradeException("restore db: {$e->getMessage()}", 0, $e) : $e;
            }
        } finally {
            fclose($in);
        }
        $errs = [];
        foreach (self::SIDECARS as $suffix) {
            $p = $this->dbPath . $suffix;
            if (file_exists($p) && !@unlink($p)) {
                $errs[] = "remove stale $p: " . FsUtil::lastError();
            }
        }
        self::syncDir($dir);
        if ($errs !== []) {
            throw new UpgradeException("restore: {$this->dbPath} replaced: " . implode('; ', $errs));
        }
    }

    /**
     * Takes an exclusive `flock` on `<data dir>/<name>/.lock` (the directory
     * is created), polling a non-blocking try every 50ms until it is held or
     * `$cancel` is cancelled (`CanceledException`, nothing held). The file
     * is never deleted.
     */
    public function lock(?Cancellation $cancel = null): Closure
    {
        $dir = $this->stateDir();
        try {
            FsUtil::mkdir($dir);
        } catch (UpgradeException $e) {
            throw new UpgradeException("create lock dir: {$e->getMessage()}", 0, $e);
        }
        $path = "$dir/.lock";
        $fh = @fopen($path, 'c');
        if ($fh === false) {
            throw new UpgradeException("lock $path: " . FsUtil::lastError());
        }
        while (true) {
            if ($cancel !== null && $cancel->cancelled()) {
                fclose($fh);
                throw new CanceledException();
            }
            if (flock($fh, LOCK_EX | LOCK_NB)) {
                break;
            }
            usleep(self::LOCK_POLL_US);
        }

        return static function () use (&$fh): void {
            if (is_resource($fh)) {
                flock($fh, LOCK_UN);
                fclose($fh);
            }
            $fh = null;
        };
    }

    /** `<data dir>/<name>`, beside the migrator's version file. */
    private function stateDir(): string
    {
        $base = $this->dataDir !== '' ? $this->dataDir : VersionFile::dataDir($this->tool);

        return FsUtil::clean("$base/{$this->name}");
    }

    private function backupName(): string
    {
        return $this->dbPath !== '' ? basename($this->dbPath) : $this->name . '.db';
    }

    /** The target is a bound parameter: no path needs quoting. */
    private function vacuumInto(string $file): void
    {
        $own = null;
        $db = $this->db;
        if ($db === null) {
            if (!extension_loaded('pdo_sqlite')) {
                throw new UpgradeException('the sqlite upgrade driver needs the pdo_sqlite extension');
            }
            // 1000 = SQLITE_ATTR_OPEN_FLAGS, 2 = SQLITE_OPEN_READWRITE (no create): the
            // named constants are deprecated in 8.5 and the replacements missing in 8.4.
            $db = $own = new PDO('sqlite:' . $this->dbPath, null, null, [
                PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION,
                PDO::ATTR_TIMEOUT => 5,
                1000 => 2,
            ]);
        }
        try {
            $st = $db->prepare('VACUUM INTO ?');
            // Silent or warning error mode on the caller's handle: false, not an exception.
            if ($st === false || !$st->execute([$file])) {
                throw new UpgradeException(($st === false ? (string) ($db->errorInfo()[2] ?? '') : (string) ($st->errorInfo()[2] ?? '')) ?: 'VACUUM INTO failed');
            }
        } catch (PDOException $e) {
            throw new UpgradeException($e->getMessage(), 0, $e);
        } finally {
            $st = null;
            $db = null;
            $own = null;
        }
    }

    /** @param resource $in */
    private static function fill(string $tmp, $in, int $mode): void
    {
        $out = @fopen($tmp, 'wb');
        if ($out === false) {
            throw new UpgradeException(FsUtil::lastError());
        }
        $ok = @stream_copy_to_stream($in, $out) !== false && fflush($out) && (!function_exists('fsync') || fsync($out));
        fclose($out);
        if (!$ok || !@chmod($tmp, $mode)) {
            throw new UpgradeException(FsUtil::lastError());
        }
    }

    /** Best effort: not every platform lets a directory be opened for fsync. */
    private static function syncDir(string $dir): void
    {
        $h = @fopen($dir, 'rb');
        if ($h !== false) {
            if (function_exists('fsync')) {
                @fsync($h);
            }
            fclose($h);
        }
    }
}
