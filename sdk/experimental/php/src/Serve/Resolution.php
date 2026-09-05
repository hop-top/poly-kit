<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

use HopTop\Kit\Output\CliError;

/** The result of resolving a `serve` invocation against a registry. */
final class Resolution
{
    /**
     * @param list<string> $selected Identifiers to run, in registration
     *        order. Empty when $error is set.
     * @param bool $explicit True when the selector form was used. Under
     *        it $selected holds exactly one name and aggregate
     *        enablement was overridden rather than consulted.
     * @param list<string> $skipped Configured-but-disabled services the
     *        supervisor form passed over. Skipping is not an error and
     *        must not affect the exit code.
     */
    public function __construct(
        public readonly array $selected = [],
        public readonly bool $explicit = false,
        public readonly array $skipped = [],
        public readonly ?CliError $error = null,
        public readonly ?Outcome $outcome = null,
    ) {
    }

    public function failed(): bool
    {
        return $this->error !== null;
    }
}
