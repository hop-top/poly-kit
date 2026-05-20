<?php

declare(strict_types=1);

namespace HopTop\Kit\Output\Formatter;

/**
 * One column of a row payload: header (user-visible label, also matched
 * against --cols), key (lookup on the row), priority (hide-on-overflow).
 *
 * Mirrors py ColumnSpec and ts ColumnSpec.
 */
final class ColumnSpec
{
    public function __construct(
        public readonly string $header,
        public readonly string $key,
        public readonly int $priority = 5,
    ) {
    }

    /**
     * Named-arg-friendly factory mirroring the Py/TS construction sites.
     */
    public static function of(string $header, string $key, int $priority = 5): self
    {
        return new self($header, $key, $priority);
    }
}
