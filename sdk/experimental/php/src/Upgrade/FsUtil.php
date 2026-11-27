<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

/**
 * Filesystem helpers that throw instead of warning.
 *
 * @internal
 */
final class FsUtil
{
    /** Creates `$dir` and its parents (`mkdir -p`). */
    public static function mkdir(string $dir, int $mode = 0750): void
    {
        if (is_dir($dir)) {
            return;
        }
        if (!@mkdir($dir, $mode, true) && !is_dir($dir)) {
            throw new UpgradeException("mkdir $dir: " . self::lastError());
        }
    }

    /** Writes `$data`; `$mode` applies when the file is created. */
    public static function put(string $path, string $data, int $mode = 0600): void
    {
        $new = !file_exists($path);
        if (@file_put_contents($path, $data) === false) {
            throw new UpgradeException("write $path: " . self::lastError());
        }
        if ($new) {
            @chmod($path, $mode);
        }
    }

    /** Removes a file or a tree; a path that does not exist is fine. */
    public static function removeAll(string $path): void
    {
        if (is_link($path) || (!is_dir($path) && file_exists($path))) {
            if (!@unlink($path)) {
                throw new UpgradeException("remove $path: " . self::lastError());
            }

            return;
        }
        if (!is_dir($path)) {
            return;
        }
        $names = @scandir($path);
        if ($names === false) {
            throw new UpgradeException("remove $path: " . self::lastError());
        }
        foreach ($names as $n) {
            if ($n !== '.' && $n !== '..') {
                self::removeAll($path . DIRECTORY_SEPARATOR . $n);
            }
        }
        if (!@rmdir($path)) {
            throw new UpgradeException("remove $path: " . self::lastError());
        }
    }

    /** Lexical path clean: collapse `//`, `.` and `..`, like Go's `filepath.Clean`. */
    public static function clean(string $path): string
    {
        $abs = str_starts_with($path, '/');
        $out = [];
        foreach (explode('/', $path) as $seg) {
            if ($seg === '' || $seg === '.') {
                continue;
            }
            if ($seg === '..') {
                if ($out !== [] && end($out) !== '..') {
                    array_pop($out);
                } elseif (!$abs) {
                    $out[] = '..';
                }
                continue;
            }
            $out[] = $seg;
        }
        $s = implode('/', $out);

        return $abs ? '/' . $s : ($s === '' ? '.' : $s);
    }

    public static function lastError(): string
    {
        return error_get_last()['message'] ?? 'unknown error';
    }
}
