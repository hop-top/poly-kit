<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade\Driver;

use HopTop\Kit\Upgrade\FsUtil;
use HopTop\Kit\Upgrade\SchemaDriver;
use HopTop\Kit\Upgrade\UpgradeException;
use HopTop\Kit\Upgrade\VersionFile;
use Phar;
use PharData;
use RecursiveIteratorIterator;

/**
 * Schema driver for a directory. The migrator keeps the version file;
 * `version()` reads it back from `$dataDir` or the default data dir of
 * `$tool`.
 *
 * `backup` writes `<name>.tar.gz` into the backup directory, the same layout
 * as the Go driver, so a backup taken by one port restores in another: entry
 * names relative to the directory, directories included with their
 * permissions, no `.` root entry (restore ignores it). Symlinks and other
 * special files fail the backup. Nothing is written when the directory does
 * not exist. `restore` checks every entry name first (`path traversal
 * blocked: <name>` before anything is touched), then replaces the directory
 * with the archive's content: directories get their archived permissions,
 * files the process default. Needs the `phar` and `zlib` extensions;
 * `phar.readonly` does not apply to tar archives.
 */
final class FsDir implements SchemaDriver
{
    public function __construct(
        private readonly string $name,
        private readonly string $dirPath,
        private readonly string $tool = '',
        private readonly string $dataDir = '',
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

    public function backup(string $dest): void
    {
        self::needExtensions();
        FsUtil::mkdir($dest);
        if (!file_exists($this->dirPath)) {
            return; // nothing to back up
        }
        $tar = "$dest/." . $this->name . '.' . bin2hex(random_bytes(4)) . '.tar';
        try {
            $phar = new PharData($tar);
            $this->add($phar, $this->dirPath, '');
            $phar->stopBuffering();
            unset($phar);
            self::gzip($tar, "$dest/{$this->name}.tar.gz");
        } catch (\PharException | \UnexpectedValueException | \BadMethodCallException $e) {
            throw new UpgradeException("create archive: {$e->getMessage()}", 0, $e);
        } finally {
            self::discard($tar);
        }
    }

    public function restore(string $src): void
    {
        self::needExtensions();
        $archive = "$src/{$this->name}.tar.gz";
        if (!file_exists($archive)) {
            throw new UpgradeException("backup archive not found: $archive");
        }
        $tar = sys_get_temp_dir() . '/kit-fsdir-' . bin2hex(random_bytes(6)) . '.tar';
        try {
            self::gunzip($archive, $tar);
            foreach (TarNames::of($tar) as $n) {
                $this->target($n);
            }
            FsUtil::removeAll($this->dirPath);
            FsUtil::mkdir($this->dirPath);
            $this->extract($tar);
        } catch (\PharException | \UnexpectedValueException $e) {
            throw new UpgradeException("open archive: {$e->getMessage()}", 0, $e);
        } finally {
            self::discard($tar);
        }
    }

    /** Deletes a temporary tar and drops it from Phar's per-process cache. */
    private static function discard(string $tar): void
    {
        if (!file_exists($tar)) {
            return;
        }
        try {
            Phar::unlinkArchive($tar);
        } catch (\Throwable) {
            @unlink($tar); // not a readable archive: Phar cannot unlink it
        }
    }

    private static function needExtensions(): void
    {
        if (!class_exists(PharData::class) || !function_exists('gzopen')) {
            throw new UpgradeException('the fsdir driver needs the phar and zlib extensions');
        }
    }

    /** Adds `$dir`'s content to the archive, depth first in name order. */
    private function add(PharData $phar, string $dir, string $rel): void
    {
        $names = @scandir($dir);
        if ($names === false) {
            throw new UpgradeException("read $dir: " . FsUtil::lastError());
        }
        foreach ($names as $n) {
            if ($n === '.' || $n === '..') {
                continue;
            }
            $path = "$dir/$n";
            $entry = $rel === '' ? $n : "$rel/$n";
            $perms = (@fileperms($path) ?: 0) & 0777;
            if (is_link($path)) {
                throw new UpgradeException("unsupported entry (symlink): $entry");
            }
            if (is_dir($path)) {
                $phar->addEmptyDir($entry);
                $phar[$entry]->chmod($perms);
                $this->add($phar, $path, $entry);
            } elseif (is_file($path)) {
                $phar->addFile($path, $entry);
                $phar[$entry]->chmod($perms);
            } else {
                throw new UpgradeException("unsupported entry (special file): $entry");
            }
        }
    }

    private function extract(string $tar): void
    {
        $phar = new PharData($tar);
        $prefix = 'phar://' . (realpath($tar) ?: $tar) . '/';
        foreach (new RecursiveIteratorIterator($phar, RecursiveIteratorIterator::SELF_FIRST) as $f) {
            $name = substr((string) $f->getPathname(), strlen($prefix));
            $target = $this->target($name);
            if ($f->isDir()) {
                FsUtil::mkdir($target, ($f->getPerms() & 0777));
                continue;
            }
            FsUtil::mkdir(dirname($target));
            $in = @fopen((string) $f->getPathname(), 'rb');
            $out = @fopen($target, 'wb');
            if ($in === false || $out === false) {
                throw new UpgradeException("extract $name: " . FsUtil::lastError());
            }
            stream_copy_to_stream($in, $out);
            fclose($in);
            fclose($out);
        }
    }

    /** Where an archive entry goes; refuses names that leave the directory. */
    private function target(string $name): string
    {
        $root = FsUtil::clean($this->dirPath);
        $t = FsUtil::clean("$root/$name");
        if ($t !== $root && !str_starts_with($t, rtrim($root, '/') . '/')) {
            throw new UpgradeException("path traversal blocked: $name");
        }

        return $t;
    }

    private static function gzip(string $from, string $to): void
    {
        $in = fopen($from, 'rb');
        $out = @gzopen($to, 'wb');
        if ($in === false || $out === false) {
            throw new UpgradeException("create archive: $to");
        }
        while (!feof($in)) {
            gzwrite($out, (string) fread($in, 1 << 20));
        }
        fclose($in);
        gzclose($out);
    }

    private static function gunzip(string $from, string $to): void
    {
        $in = @gzopen($from, 'rb');
        $out = @fopen($to, 'wb');
        if ($in === false || $out === false) {
            throw new UpgradeException("open archive: $from");
        }
        while (!gzeof($in)) {
            $chunk = gzread($in, 1 << 20);
            if ($chunk === false) {
                throw new UpgradeException("gzip reader: $from");
            }
            fwrite($out, $chunk);
        }
        gzclose($in);
        fclose($out);
    }
}
