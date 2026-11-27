<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

/** Outcome of `DocumentMigrator::migrate`: the JSON text and the report. */
final readonly class DocResult
{
    public function __construct(
        public string $document,
        public DocReport $report,
    ) {}
}
