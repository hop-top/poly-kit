<?php

declare(strict_types=1);

namespace HopTop\Kit\Output\Formatter;

use InvalidArgumentException;

/**
 * Validate raw `key=value` pairs against $specs and return the coerced map.
 *
 * Mirrors py output.formatter.parse_options, ts parseOptions, and go
 * ParseOptions.
 *
 * Coercion rules:
 * - 'string' : raw value passed through.
 * - 'int'    : intval() with hard-fail on non-numeric input.
 * - 'bool'   : true|false|1|0|yes|no|t|f|y|n (case-insensitive).
 * - 'enum'   : value must be in spec.enum.
 * - key-only form (no '=') valid only for type === Bool → true.
 * - unknown keys throw InvalidArgumentException listing the valid set.
 * - defaults from specs fill in any keys not present in $pairs.
 */
final class Options
{
    /**
     * @param list<string>     $pairs raw "key=value" or "key" entries (from --format-opt)
     * @param list<OptionSpec> $specs accepted option specs
     *
     * @return array<string,mixed>
     */
    public static function parse(array $pairs, array $specs): array
    {
        $byName = [];
        foreach ($specs as $s) {
            $byName[$s->name] = $s;
        }

        $out = [];
        foreach ($pairs as $raw) {
            if (str_contains($raw, '=')) {
                [$key, $val] = explode('=', $raw, 2);
                $hasEq = true;
            } else {
                $key = $raw;
                $val = '';
                $hasEq = false;
            }
            $key = trim($key);
            if ($key === '') {
                throw new InvalidArgumentException(sprintf('empty option key in %s', var_export($raw, true)));
            }
            if (!isset($byName[$key])) {
                $valid = implode(', ', array_map(static fn (OptionSpec $s) => $s->name, $specs));
                throw new InvalidArgumentException(sprintf("unknown option '%s' (valid: %s)", $key, $valid));
            }
            $spec = $byName[$key];
            if (!$hasEq) {
                if ($spec->type !== OptionType::Bool) {
                    throw new InvalidArgumentException(sprintf("option '%s' requires a value (e.g. %s=...)", $key, $key));
                }
                $out[$key] = true;
                continue;
            }
            $out[$key] = self::coerce($spec, $val);
        }

        foreach ($specs as $s) {
            if (array_key_exists($s->name, $out)) {
                continue;
            }
            if ($s->default !== null) {
                $out[$s->name] = $s->default;
            }
        }

        return $out;
    }

    private static function coerce(OptionSpec $spec, string $val): mixed
    {
        switch ($spec->type) {
            case OptionType::String:
                return $val;
            case OptionType::Int:
                if ($val === '' || !preg_match('/^-?\d+$/', $val)) {
                    throw new InvalidArgumentException(sprintf("option '%s': '%s' is not an int", $spec->name, $val));
                }
                return (int) $val;
            case OptionType::Bool:
                $low = strtolower(trim($val));
                if (in_array($low, ['true', '1', 'yes', 't', 'y'], true)) {
                    return true;
                }
                if (in_array($low, ['false', '0', 'no', 'f', 'n'], true)) {
                    return false;
                }
                throw new InvalidArgumentException(sprintf("option '%s': '%s' is not a bool", $spec->name, $val));
            case OptionType::Enum:
                if (in_array($val, $spec->enum, true)) {
                    return $val;
                }
                throw new InvalidArgumentException(sprintf("option '%s': '%s' not in {%s}", $spec->name, $val, implode(', ', $spec->enum)));
        }
    }
}
