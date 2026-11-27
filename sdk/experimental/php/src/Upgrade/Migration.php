<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

use Closure;

/**
 * One versioned schema migration.
 *
 * `up` is required (a migration that cannot go forward is not one). `down`
 * null means no rollback. `verify`, when set, runs after `up` and before the
 * version is recorded; a failure counts as a failed step (`VerifyException`)
 * and the rollback includes this migration. Each closure signals failure by
 * throwing.
 */
final readonly class Migration
{
    /**
     * @param string $version semver, pre-release allowed
     * @param string $schema  matches the driver's name
     * @param Closure(): void $up
     * @param (Closure(): void)|null $down
     * @param (Closure(): void)|null $verify
     */
    public function __construct(
        public string $version,
        public string $schema,
        public Closure $up,
        public ?Closure $down = null,
        public ?Closure $verify = null,
    ) {}
}
