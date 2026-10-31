<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli\Delegation;

use HopTop\Kit\Output\CliError;

/** An unrendered envelope: the gate refused, the caller renders and exits. */
final class GateRefusal extends \RuntimeException
{
    public function __construct(public readonly CliError $error)
    {
        parent::__construct((string) $error);
    }
}
