<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli\Delegation;

/** The resolved root flags. `policy` is always the root's. */
final readonly class Globals
{
    public function __construct(
        public string $confirm = '',
        public int $maxOps = 0,
        public string $policy = '',
        public bool $dryRun = false,
    ) {
    }
}
