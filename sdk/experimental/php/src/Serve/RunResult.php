<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

use HopTop\Kit\Output\CliError;

/** What one supervised run produced. */
final class RunResult
{
    /**
     * @param list<string> $started Identifiers whose start was invoked,
     *        in invocation order.
     * @param list<string> $ready Identifiers that reported ready, in
     *        report order.
     * @param array<string, string> $failed Identifier to the error it
     *        failed with.
     */
    public function __construct(
        public readonly Outcome $outcome,
        public readonly ?CliError $error = null,
        public readonly array $started = [],
        public readonly array $ready = [],
        public readonly array $failed = [],
    ) {
    }

    public function exitCode(): int
    {
        return $this->error !== null ? $this->error->exitCode : $this->outcome->exitCode();
    }
}
