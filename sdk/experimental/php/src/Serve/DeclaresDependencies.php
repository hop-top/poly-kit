<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

/**
 * Ordering declaration. Start order is topological over these, ties
 * broken by registration order; stop order is the exact reverse of the
 * order services actually started.
 */
interface DeclaresDependencies
{
    /** @return list<string> */
    public function dependsOn(): array;
}
