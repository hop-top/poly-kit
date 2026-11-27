<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Upgrade\Support;

use HopTop\Kit\Upgrade\VersionStorer;

/** A locking recording driver that keeps its own version. */
final class LockingStoringDriver extends LockingDriver implements VersionStorer
{
    public function storesVersion(): bool
    {
        return true;
    }
}
