<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Upgrade\Support;

/** A scratch directory per test, removed (read-only subtrees included) afterwards. */
trait TempDir
{
    private string $tmp = '';

    protected function setUp(): void
    {
        $this->tmp = sys_get_temp_dir() . '/kit-upg-' . bin2hex(random_bytes(6));
        mkdir($this->tmp, 0750, true);
    }

    protected function tearDown(): void
    {
        self::rmrf($this->tmp);
    }

    private static function rmrf(string $path): void
    {
        if (is_link($path) || is_file($path)) {
            @unlink($path);

            return;
        }
        if (is_dir($path)) {
            @chmod($path, 0750);
            foreach ((array) scandir($path) as $n) {
                if ($n !== '.' && $n !== '..') {
                    self::rmrf("$path/$n");
                }
            }
            @rmdir($path);
        }
    }
}
