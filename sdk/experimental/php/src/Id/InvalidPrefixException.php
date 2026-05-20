<?php

declare(strict_types=1);

namespace HopTop\Kit\Id;

/**
 * Thrown when a prefix fails TypeID v0.3 grammar.
 *
 * Spec: `^[a-z]([a-z0-9_]*[a-z0-9])?$`, max 63 chars.
 */
final class InvalidPrefixException extends IdException {}
