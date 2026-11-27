<?php

declare(strict_types=1);

namespace HopTop\Kit\Upgrade;

/** The run's `Cancellation` was cancelled. The message is `context canceled`, as in the other ports. */
final class CanceledException extends UpgradeException
{
    public function __construct(string $message = 'context canceled')
    {
        parent::__construct($message);
    }
}
