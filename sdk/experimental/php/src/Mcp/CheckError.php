<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/** One failed validation step in the modern chain. */
final readonly class CheckError
{
    public function __construct(
        public int $code,
        public string $message,
        public int $status,
        public mixed $data = null,
    ) {
    }
}
