<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Upgrade\Support;

use HopTop\Kit\Upgrade\VersionStorer;

/** A recording driver that keeps its own version. */
class StoringDriver extends RecordingDriver implements VersionStorer
{
    public function storesVersion(): bool
    {
        return true;
    }
}
