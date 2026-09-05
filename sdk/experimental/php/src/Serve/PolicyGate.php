<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

/**
 * The third validation gate. A service whose declared class the gate
 * denies is refused at UNAUTHORIZED, exit 5.
 *
 * The contract requires the gate, not Go's YAML-driven
 * side_effect × network table: "a port satisfies the gate with a
 * two-argument predicate; a port that has wired no policy at all
 * passes every service, exactly as Go does with a nil gate."
 */
interface PolicyGate
{
    /**
     * Whether a service of this class may run. Returning a reason
     * alongside the refusal lets the message name the rule that fired.
     */
    public function allow(string $sideEffect, string $network): PolicyVerdict;
}
