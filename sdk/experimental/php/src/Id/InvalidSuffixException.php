<?php

declare(strict_types=1);

namespace HopTop\Kit\Id;

/**
 * Thrown when a TypeID suffix is malformed or its decoded value overflows 128 bits.
 *
 * Spec: 26 Crockford base32 chars, first char <= '7' so the encoded value
 * fits in 128 bits.
 */
final class InvalidSuffixException extends IdException {}
