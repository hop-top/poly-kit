<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

/**
 * The version file a migrator keeps for a driver that does not store its own
 * version: `<data dir>/<schema>/version`, where the data dir is
 * `$XDG_DATA_HOME/hop/<tool>` (OS-native default when unset: `~/.local/share`,
 * `~/Library/Application Support` on macOS, `%LOCALAPPDATA%` on Windows) or
 * the `dataDir` given to the migrator. Backups live beside it under
 * `<data dir>/backups/<schema>/pre-<version>/`. Same layout in every port.
 */
final class VersionFile
{
    /** The default data dir of a tool. */
    public static function dataDir(string $tool): string
    {
        $home = getenv('XDG_DATA_HOME');
        if (is_string($home) && $home !== '' && str_starts_with($home, '/')) {
            $base = $home;
        } else {
            $h = getenv('HOME');
            $local = getenv('LOCALAPPDATA');
            if (PHP_OS_FAMILY === 'Windows' && is_string($local) && $local !== '') {
                $base = $local;
            } elseif (!is_string($h) || $h === '') {
                throw new UpgradeException('resolve data dir: $HOME is not set');
            } else {
                $base = PHP_OS_FAMILY === 'Darwin' ? "$h/Library/Application Support" : "$h/.local/share";
            }
        }

        return FsUtil::clean("$base/hop/$tool");
    }

    /** The version for `$tool`'s `$schema` in the default data dir; '' when no file. */
    public static function read(string $tool, string $schema): string
    {
        return self::readIn(self::dataDir($tool), $schema);
    }

    /** The version in `<dataDir>/<schema>/version`; '' when no file. */
    public static function readIn(string $dataDir, string $schema): string
    {
        $file = "$dataDir/$schema/version";
        if (!file_exists($file)) {
            return '';
        }
        $text = @file_get_contents($file);
        if ($text === false) {
            throw new UpgradeException("read $file: " . FsUtil::lastError());
        }

        return trim($text);
    }

    /** Writes `<dataDir>/<schema>/version`. */
    public static function write(string $dataDir, string $schema, string $version): void
    {
        $dir = "$dataDir/$schema";
        try {
            FsUtil::mkdir($dir);
        } catch (UpgradeException $e) {
            throw new UpgradeException("create version dir: {$e->getMessage()}", 0, $e);
        }
        try {
            FsUtil::put("$dir/version", $version);
        } catch (UpgradeException $e) {
            throw new UpgradeException("write version file: {$e->getMessage()}", 0, $e);
        }
    }
}
