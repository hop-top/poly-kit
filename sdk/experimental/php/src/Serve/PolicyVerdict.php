<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

/** One policy decision, with the reason a refusal fired. */
final class PolicyVerdict
{
    private function __construct(
        public readonly bool $ok,
        public readonly string $reason = '',
    ) {
    }

    public static function allow(): self
    {
        return new self(true);
    }

    public static function deny(string $reason = ''): self
    {
        return new self(false, $reason);
    }
}
