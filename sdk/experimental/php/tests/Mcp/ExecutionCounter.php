<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Mcp;

/**
 * Counts leaf executions across a multi-round-trip exchange.
 *
 * A mutable holder rather than a captured local, so the runner closure and
 * the assertions can read the same tally without passing references around.
 */
final class ExecutionCounter
{
    public int $n = 0;
}
