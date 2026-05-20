<?php

declare(strict_types=1);

namespace HopTop\Kit\Id;

/**
 * Decomposed view of a parsed TypeID.
 *
 * - `$prefix`: the entity-type label (empty string if the source was bare).
 * - `$uuid`:   the canonical hyphenated UUIDv7 string the suffix encodes.
 *
 * Round-trip invariant: `Id::new($prefix) -> $s; Id::parse($s)` yields
 * `ParsedId(prefix=$prefix, uuid=<the underlying uuid7>)`.
 */
final readonly class ParsedId
{
    public function __construct(
        public string $prefix,
        public string $uuid,
    ) {}
}
