<?php

declare(strict_types=1);

namespace HopTop\KitEngine\Tests;

use PHPUnit\Framework\TestCase;

final class SmokeTest extends TestCase
{
    public function testAutoloadRootIsPsr4Mapped(): void
    {
        $loader = require __DIR__ . '/../vendor/autoload.php';
        $dirs = $loader->getPrefixesPsr4()['HopTop\\KitEngine\\'] ?? [];
        self::assertNotEmpty($dirs);
        self::assertSame(realpath(__DIR__ . '/../src'), realpath($dirs[0]));
    }
}
