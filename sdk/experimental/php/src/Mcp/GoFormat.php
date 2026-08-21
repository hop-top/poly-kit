<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * Renders values the way Go's `fmt` verbs do.
 *
 * Header-mismatch messages interpolate values with `%q` and `%v`, and the
 * exact text is fixture-pinned, so the quoting rules are part of the wire
 * contract rather than cosmetic.
 */
final class GoFormat
{
    /**
     * Go's `%q`: a double-quoted string with Go escape sequences.
     */
    public static function quote(string $value): string
    {
        $out = '';

        for ($i = 0; $i < \strlen($value); ++$i) {
            $char = $value[$i];

            $out .= match ($char) {
                '"' => '\\"',
                '\\' => '\\\\',
                "\n" => '\\n',
                "\t" => '\\t',
                "\r" => '\\r',
                default => \ord($char) < 0x20 ? \sprintf('\\x%02x', \ord($char)) : $char,
            };
        }

        return '"'.$out.'"';
    }

    /**
     * Go's `%v` for the decoded JSON values a `_meta` member can hold.
     *
     * Strings print bare, booleans as `true`/`false`, null as `<nil>`,
     * and whole floats without a fractional part — matching how Go's
     * default formatting renders a `float64` decoded from JSON.
     */
    public static function value(mixed $value): string
    {
        if (null === $value) {
            return '<nil>';
        }

        if (\is_bool($value)) {
            return $value ? 'true' : 'false';
        }

        if (\is_string($value)) {
            return $value;
        }

        if (\is_int($value)) {
            return (string) $value;
        }

        if (\is_float($value)) {
            return $value === floor($value) && is_finite($value)
                ? (string) (int) $value
                : (string) $value;
        }

        if (\is_array($value)) {
            return self::renderComposite($value);
        }

        return (string) json_encode($value);
    }

    /**
     * Go prints a decoded object as `map[k:v ...]` with sorted keys, and a
     * decoded array as `[a b ...]`.
     *
     * @param array<array-key, mixed> $value
     */
    private static function renderComposite(array $value): string
    {
        if (array_is_list($value)) {
            return '['.implode(' ', array_map(self::value(...), $value)).']';
        }

        ksort($value, \SORT_STRING);

        $parts = [];
        foreach ($value as $key => $item) {
            $parts[] = $key.':'.self::value($item);
        }

        return 'map['.implode(' ', $parts).']';
    }
}
