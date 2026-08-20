<?php

declare(strict_types=1);

namespace HopTop\Kit\Output\Formatter;

use InvalidArgumentException;

/**
 * One column of a row payload: header (user-visible label, matched against
 * --cols AND used as the row lookup), key (redundant alias of header),
 * priority (hide-on-overflow).
 *
 * header == key, universally. Validation and value lookup are the same
 * operation on the same name. Go cannot express header != key through its
 * `table:""` struct tags — the tag supplies the header and the lookup comes
 * from the struct field — so no SDK may. The constructor enforces it so
 * drift is impossible.
 *
 * priority is accepted and stored but ignored by the payload SDKs: the
 * hide-on-overflow feature it drives is implemented in Go only today.
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
        if ($header !== $key) {
            throw new InvalidArgumentException(sprintf(
                "ColumnSpec header '%s' must equal key '%s'",
                $header,
                $key,
            ));
        }
    }

    /**
     * Named-arg-friendly factory mirroring the Py/TS construction sites.
     */
    public static function of(string $header, string $key, int $priority = 5): self
    {
        return new self($header, $key, $priority);
    }
}
