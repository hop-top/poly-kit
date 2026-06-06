<?php

declare(strict_types=1);

namespace HopTop\Kit\Id;

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
 * compose poly-URIs through the `hop-top/cite` package instead.
 */
final class Id
{
    /**
     * TypeID v0.3 prefix grammar (matches `jewei/typeid-php` Validator):
     * lowercase a-z plus underscore separators, no leading or trailing
     * underscore, max 63 chars. Empty string is valid (no prefix).
     */
    private const string PREFIX_PATTERN = '/^([a-z]([a-z_]{0,61}[a-z])?)?$/';

    private const int PREFIX_MAX_LENGTH = 63;

    private function __construct() {}

    /**
     * Generate a new TypeID with the given prefix.
     *
     * @throws InvalidPrefixException If $prefix violates the TypeID v0.3 grammar.
     */
    public static function new(string $prefix): string
    {
        self::validatePrefix($prefix);

        // Suffix is generated internally from a fresh UUIDv7; the only
        // remaining failure mode is an upstream UUIDv7 generator hiccup,
        // which surfaces as ConstructorException and is genuinely a
        // suffix-side issue (bad random bytes).
        try {
            return TypeID::generate($prefix)->toString();
        } catch (ConstructorException $e) {
            throw new InvalidSuffixException(
                "failed to generate TypeID suffix for prefix: {$prefix}",
                previous: $e,
            );
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
        if ($s === '') {
            throw new InvalidSuffixException('cannot parse empty TypeID string');
        }

        // Split locally so we can decide which kit exception to raise without
        // resorting to substring-matching upstream error messages.
        $lastUnderscore = strrpos($s, '_');
        if ($lastUnderscore === 0) {
            throw new InvalidPrefixException(
                "TypeID string cannot start with underscore: {$s}",
            );
        }

        $prefix = $lastUnderscore === false ? '' : substr($s, 0, $lastUnderscore);
        self::validatePrefix($prefix);

        try {
            $tid = TypeID::fromString($s);
        } catch (ConstructorException $e) {
            // Prefix already validated locally — any remaining upstream
            // failure must concern the suffix (length, alphabet, overflow).
            throw new InvalidSuffixException(
                "invalid TypeID suffix in: {$s}",
                previous: $e,
            );
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
        self::validatePrefix($prefix);

        try {
            return TypeID::fromUuid($uuid, $prefix)->toString();
        } catch (ValidationException $e) {
            // Prefix was already validated above; any remaining ValidationException
            // means the encoded suffix failed Validator::isValidSuffix, which is
            // a suffix-side problem traceable to a bad UUID.
            throw new InvalidSuffixException(
                "invalid UUID for TypeID suffix: {$uuid}",
                previous: $e,
            );
        } catch (ConstructorException $e) {
            throw new InvalidSuffixException(
                "invalid UUID for TypeID suffix: {$uuid}",
                previous: $e,
            );
        }
    }

    /**
     * Validate a TypeID prefix against the v0.3 grammar.
     *
     * Local check that mirrors `TypeID\Validator::isValidPrefix` so kit-side
     * callers get a typed `InvalidPrefixException` without relying on
     * substring-matching upstream exception messages — which would silently
     * break if the upstream library rephrased its errors.
     *
     * @throws InvalidPrefixException
     */
    public static function validatePrefix(string $prefix): void
    {
        if ($prefix === '') {
            return;
        }

        if (strlen($prefix) > self::PREFIX_MAX_LENGTH) {
            throw new InvalidPrefixException(sprintf(
                'invalid TypeID prefix (max %d chars): %s',
                self::PREFIX_MAX_LENGTH,
                $prefix,
            ));
        }

        if (preg_match(self::PREFIX_PATTERN, $prefix) !== 1) {
            throw new InvalidPrefixException("invalid TypeID prefix: {$prefix}");
        }
    }
}
