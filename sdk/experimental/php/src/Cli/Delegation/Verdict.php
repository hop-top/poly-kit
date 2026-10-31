<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli\Delegation;

final readonly class Verdict
{
    public function __construct(
        public bool $allowed,
        public bool $requireConfirm = false,
        public string $reason = '',
    ) {
    }
}
