<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

/** Says what a document migration did. */
final readonly class DocReport
{
    /** @param list<DocApplied> $applied */
    public function __construct(
        public int $from,
        public int $to,
        public array $applied,
    ) {}
}
