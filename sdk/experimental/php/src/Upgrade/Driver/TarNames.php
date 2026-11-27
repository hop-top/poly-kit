<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade\Driver;

use HopTop\Kit\Upgrade\UpgradeException;

/**
 * The entry names of an uncompressed tar file, read from its headers (ustar
 * name and prefix, GNU long names, pax `path`). `PharData` hides entries whose
 * names climb out of the archive, so the restore guard reads the names here.
 *
 * @internal
 */
final class TarNames
{
    /** @return list<string> */
    public static function of(string $tarPath): array
    {
        $f = @fopen($tarPath, 'rb');
        if ($f === false) {
            throw new UpgradeException("open archive: $tarPath");
        }
        $names = [];
        $next = null;
        try {
            while (strlen($h = (string) fread($f, 512)) === 512) {
                if (trim($h, "\0") === '') {
                    break;
                }
                $size = self::size(substr($h, 124, 12));
                $type = $h[156];
                $skip = (int) (ceil($size / 512) * 512);
                if ($type === 'L' || $type === 'x') {
                    $body = $size > 0 ? (string) fread($f, $size) : '';
                    $skip -= strlen($body);
                    $next = $type === 'L' ? rtrim($body, "\0") : (self::paxPath($body) ?? $next);
                } elseif ($type !== 'g') {
                    $name = rtrim(substr($h, 0, 100), "\0");
                    $prefix = substr($h, 257, 5) === 'ustar' ? rtrim(substr($h, 345, 155), "\0") : '';
                    $names[] = $next ?? ($prefix === '' ? $name : "$prefix/$name");
                    $next = null;
                }
                if ($skip > 0) {
                    fseek($f, $skip, SEEK_CUR);
                }
            }
        } finally {
            fclose($f);
        }

        return $names;
    }

    private static function size(string $field): int
    {
        if ((ord($field[0]) & 0x80) !== 0) {
            $n = ord($field[0]) & 0x7f;
            for ($i = 1; $i < 12; $i++) {
                $n = ($n << 8) | ord($field[$i]);
            }

            return $n;
        }

        return (int) octdec(trim($field, "\0 "));
    }

    private static function paxPath(string $body): ?string
    {
        $path = null;
        while ($body !== '' && ($sp = strpos($body, ' ')) !== false) {
            $len = (int) substr($body, 0, $sp);
            if ($len <= $sp) {
                break;
            }
            $rec = substr($body, $sp + 1, $len - $sp - 2);
            if (str_starts_with($rec, 'path=')) {
                $path = substr($rec, 5);
            }
            $body = substr($body, $len);
        }

        return $path;
    }
}
