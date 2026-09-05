<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

/**
 * The narrow slice of a logger the supervisor needs.
 *
 * The contract makes the *sink* conditional but the surfacing
 * mandatory: "at least one MUST, and the vocabulary below is fixed
 * whichever does". This SDK has no event bus, so the log is that one
 * sink, and a supervisor is never constructed without one.
 *
 * @see StderrLogger for the default this SDK ships.
 */
interface ServeLogger
{
    /**
     * @param array<string, scalar|null> $fields Structured fields, not
     *        text interpolated into $message.
     */
    public function info(string $message, array $fields = []): void;

    /** @param array<string, scalar|null> $fields */
    public function error(string $message, array $fields = []): void;
}
