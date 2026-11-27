<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

/** One step a document migration applied. */
final readonly class DocApplied
{
    public function __construct(
        public int $from,
        public int $to,
        public string $note,
    ) {}
}
