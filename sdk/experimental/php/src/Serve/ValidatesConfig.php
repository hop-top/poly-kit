<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

/**
 * The second of the three validation gates, as an optional
 * declaration a registration MAY carry.
 *
 * The contract requires these as *concepts*, and requires the
 * described effects where a service does carry one; how a port lets a
 * registration opt in is not contract. Go uses optional interfaces,
 * TypeScript optional properties. PHP takes Go's shape rather than
 * TS's, because an optional method on an interface is not expressible
 * here: a separate interface per declaration is the language's own way
 * of saying "may carry".
 */
interface ValidatesConfig
{
    /**
     * Returns an error message, or null when the resolved
     * configuration is complete and usable.
     */
    public function validateConfig(): ?string;
}
