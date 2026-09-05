<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

/**
 * Policy declaration: the `kit/side-effect` and `kit/network` classes.
 * A service that does not implement this is unclassified and passes
 * the policy gate.
 */
interface DeclaresClass
{
    /** @return array{0: string, 1: string} side effect, then network. */
    public function serviceClass(): array;
}
