<?php

declare(strict_types=1);

namespace HopTop\Kit\Id;

use Throwable;
use TypeID\Exception\ConstructorException;
use TypeID\Exception\ValidationException;
use TypeID\TypeID;

/**
 * kit's cross-language Id primitive (PHP binding).
 *
 * Thin static facade around `jewei/typeid-php` (Jetify TypeID spec v0.3.0).
 * Implements the kit-go + 4-SDK API shape defined in ADR 0001:
 *
 *   - `Id::new($prefix)`        → fresh UUIDv7-backed typeid string
 *   - `Id::parse($s)`           → ParsedId { prefix, uuid }
 *   - `Id::fromUuid($p, $uuid)` → deterministic typeid string for a given uuid
 *
 * The canonical string form is the wire form. No URI helpers live here —
 * compose poly-URIs through the `hop-top/uri` package instead.
 */
final class Id
{
    private function __construct() {}

    /**
     * Generate a new TypeID with the given prefix.
     *
     * @throws InvalidPrefixException If $prefix violates the TypeID v0.3 grammar.
     */
    public static function new(string $prefix): string
    {
        try {
            return TypeID::generate($prefix)->toString();
        } catch (ValidationException $e) {
            throw new InvalidPrefixException(
                "invalid TypeID prefix: {$prefix}",
                previous: $e,
            );
        } catch (ConstructorException $e) {
            // Upstream uses ConstructorException for any generation failure,
            // including prefix validation failures wrapped during fromUuid.
            throw self::classifyConstructorException($e, $prefix);
        }
    }

    /**
     * Parse a canonical TypeID string into a ParsedId.
     *
     * Accepts both prefixed (`task_01j…`) and bare (`01j…`) forms. The
     * returned `ParsedId::$uuid` is the canonical hyphenated UUIDv7 string.
     *
     * @throws InvalidPrefixException If the prefix violates the grammar.
     * @throws InvalidSuffixException If the suffix is malformed or overflows.
     */
    public static function parse(string $s): ParsedId
    {
        try {
            $tid = TypeID::fromString($s);
        } catch (ConstructorException $e) {
            throw self::classifyConstructorException($e, $s);
        }

        return new ParsedId(
            prefix: $tid->prefix,
            uuid: $tid->toUuid(),
        );
    }

    /**
     * Deterministic construction from an explicit UUID and prefix.
     *
     * Useful for fixtures and cross-language parity tests where both sides
     * encode the same UUIDv7 input and must agree on the output string.
     *
     * @throws InvalidPrefixException If $prefix is invalid.
     * @throws InvalidSuffixException If $uuid is not a valid UUID.
     */
    public static function fromUuid(string $prefix, string $uuid): string
    {
        try {
            return TypeID::fromUuid($uuid, $prefix)->toString();
        } catch (ValidationException $e) {
            throw new InvalidPrefixException(
                "invalid TypeID prefix: {$prefix}",
                previous: $e,
            );
        } catch (ConstructorException $e) {
            // fromUuid wraps Base32::encode failures (i.e. bad UUID input)
            // in ConstructorException.
            throw new InvalidSuffixException(
                "invalid UUID for TypeID suffix: {$uuid}",
                previous: $e,
            );
        }
    }

    /**
     * Map an upstream ConstructorException onto the kit exception hierarchy.
     *
     * `jewei/typeid-php` raises ConstructorException for any of: empty
     * input, malformed delimiter, bad prefix, bad suffix, bad UUID. We
     * inspect the message to route to the right kit-side type.
     */
    private static function classifyConstructorException(
        Throwable $e,
        string $context,
    ): IdException {
        $message = $e->getMessage();

        if (str_contains($message, 'prefix')) {
            return new InvalidPrefixException(
                "invalid TypeID prefix in: {$context}",
                previous: $e,
            );
        }

        return new InvalidSuffixException(
            "invalid TypeID suffix in: {$context}",
            previous: $e,
        );
    }
}
