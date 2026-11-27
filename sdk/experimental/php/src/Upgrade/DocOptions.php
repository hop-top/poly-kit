<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

/** Options for `DocumentMigrator`. */
final readonly class DocOptions
{
    /**
     * @param string $versionField dotted path of the integer version; `version` when empty
     * @param int $latest the version this build writes; 0 means the highest `to`
     * @param bool $missingIsZero read a missing or non-integer version as 0 instead of failing
     */
    public function __construct(
        public string $versionField = '',
        public int $latest = 0,
        public bool $missingIsZero = false,
    ) {}
}
