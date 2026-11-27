<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade\Driver;

use HopTop\Kit\Upgrade\FsUtil;
use HopTop\Kit\Upgrade\SchemaDriver;
use HopTop\Kit\Upgrade\UpgradeException;
use HopTop\Kit\Upgrade\VersionFile;

/**
 * Schema driver for one or more config files. The migrator keeps the
 * version file; `version()` reads it back from `$dataDir` (the migrator's
 * `dataDir`) or, without one, the default data dir of `$tool`.
 *
 * `backup` copies each file into the backup directory as its full path with
 * `/` replaced by `_` (`/etc/foo/config.yaml` becomes `_etc_foo_config.yaml`),
 * skipping files that do not exist; `restore` copies them back, skipping
 * files the backup does not hold. Same names and semantics as the Go driver.
 */
final class ConfigFile implements SchemaDriver
{
    /** @param list<string> $paths the files this schema covers */
    public function __construct(
        private readonly array $paths,
        private readonly string $name = 'config',
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
        FsUtil::mkdir($dest);
        foreach ($this->paths as $p) {
            $name = self::backupFileName($p);
            try {
                self::copy($p, "$dest/$name");
            } catch (MissingFile) {
                continue;
            } catch (UpgradeException $e) {
                throw new UpgradeException("backup $name: {$e->getMessage()}", 0, $e);
            }
        }
    }

    public function restore(string $src): void
    {
        foreach ($this->paths as $p) {
            $backup = "$src/" . self::backupFileName($p);
            if (!file_exists($backup)) {
                continue;
            }
            try {
                self::copy($backup, $p);
            } catch (UpgradeException $e) {
                throw new UpgradeException('restore ' . self::backupFileName($p) . ": {$e->getMessage()}", 0, $e);
            }
        }
    }

    /** Collision-safe file name derived from the full path. */
    private static function backupFileName(string $path): string
    {
        return str_replace('/', '_', FsUtil::clean($path));
    }

    /**
     * @throws MissingFile when `$src` does not exist
     * @throws UpgradeException when it cannot be copied
     */
    private static function copy(string $src, string $dst): void
    {
        if (!file_exists($src)) {
            throw new MissingFile();
        }
        $in = is_dir($src) ? false : @fopen($src, 'rb');
        if ($in === false) {
            throw new UpgradeException("open $src: " . (is_dir($src) ? 'is a directory' : FsUtil::lastError()));
        }
        try {
            FsUtil::mkdir(dirname($dst));
            $out = @fopen($dst, 'wb');
            if ($out === false) {
                throw new UpgradeException("create $dst: " . FsUtil::lastError());
            }
            try {
                if (@stream_copy_to_stream($in, $out) === false) {
                    throw new UpgradeException("copy $src: " . FsUtil::lastError());
                }
            } finally {
                fclose($out);
            }
        } finally {
            fclose($in);
        }
    }
}
