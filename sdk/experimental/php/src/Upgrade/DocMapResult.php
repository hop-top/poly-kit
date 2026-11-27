<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

/** Outcome of `DocumentMigrator::migrateArray`: the migrated array and the report. */
final readonly class DocMapResult
{
    /** @param array<mixed> $document */
    public function __construct(
        public array $document,
        public DocReport $report,
    ) {}
}
