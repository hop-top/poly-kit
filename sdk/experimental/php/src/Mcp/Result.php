<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * The outcome of invoking a command leaf.
 *
 * Ports Go's `Result`. `isError` on the wire is derived from a non-zero
 * exit code, not carried separately.
 */
final readonly class Result
{
    public function __construct(
        public string $stdout = '',
        public string $stderr = '',
        public int $exitCode = 0,
        public mixed $data = null,
    ) {
    }
}
