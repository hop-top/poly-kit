<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

/** A migration the migrator applied. */
final readonly class AppliedMigration
{
    public function __construct(
        public string $version,
        public string $schema,
    ) {}
}
