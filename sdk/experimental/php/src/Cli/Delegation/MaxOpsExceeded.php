<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli\Delegation;

/** Raised by {@see Engine::recordOp()} once the budget is spent. */
final class MaxOpsExceeded extends \RuntimeException
{
    public function __construct()
    {
        parent::__construct('policy: max-ops budget exceeded');
    }
}
