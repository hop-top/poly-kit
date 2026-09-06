<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

/**
 * Service identifier grammar and the reserved selector vocabulary.
 *
 * Mirrors the contract in docs/contracts/serve-lifecycle.md,
 * §"Cross-language parity". An identifier is a CLI word, a config key
 * segment, and an event payload value at once, which is why the
 * grammar is contract rather than a local convention.
 */
final class Names
{
    /** Lowercase ASCII, digits, and internal hyphens, starting with a letter. */
    public const string PATTERN = '/^[a-z][a-z0-9-]*$/';

    /**
     * Names reserved for selector vocabulary. Registering one would
     * make `serve <name>` ambiguous with a future aggregate form, and
     * is why `--list` is a flag rather than a `serve list` child.
     *
     * @var list<string>
     */
    public const array RESERVED = ['all', 'list', 'none'];

    /** Reports whether $name is one of the reserved selector words. */
    public static function isReserved(string $name): bool
    {
        return in_array($name, self::RESERVED, true);
    }

    /**
     * Validates a service identifier, returning an error message or
     * null. Mirrors Go's serve.ValidateName and the TS validateName.
     */
    public static function validate(string $name): ?string
    {
        if ($name === '') {
            return 'serve: service name is empty';
        }
        if (preg_match(self::PATTERN, $name) !== 1) {
            return "serve: service name \"{$name}\" must be lowercase letters, "
                . 'digits, or hyphens, starting with a letter';
        }
        if (self::isReserved($name)) {
            return "serve: service name \"{$name}\" is reserved";
        }
        return null;
    }
}
