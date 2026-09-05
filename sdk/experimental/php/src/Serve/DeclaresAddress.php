<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

/**
 * Address declaration. Read once the service reports ready and carried
 * into the readiness event under `address`, so an operator learns
 * where the service actually bound — including a port the kernel
 * picked for a wildcard address, which configuration cannot reveal.
 */
interface DeclaresAddress
{
    public function addr(): string;
}
