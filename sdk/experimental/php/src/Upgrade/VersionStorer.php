<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

/**
 * Optional marker for a driver that keeps its version itself (a database
 * table). For such a driver the migrator calls only `setVersion` to record a
 * version and writes no version file; `version()` is the one authority.
 * `storesVersion()` reports whether the marker applies.
 */
interface VersionStorer
{
    public function storesVersion(): bool;
}
