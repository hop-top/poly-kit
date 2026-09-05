<?php

declare(strict_types=1);

namespace HopTop\Kit\Serve;

/**
 * Parses the duration spelling the contract's config table uses.
 *
 * The `services.*` timeouts are typed `duration` with defaults written
 * `30s` and `60s`, and one YAML file is often read by a fleet of tools
 * that are not all the same language — so a PHP tool has to understand
 * the same spelling a Go tool wrote. Go's own `time.ParseDuration`
 * grammar is the reference: a bare number is seconds here, since a
 * config file is where an operator writes `30`, and every unit Go
 * accepts down to milliseconds is understood.
 */
final class Duration
{
    private const array UNITS = [
        'ms' => 0.001,
        's' => 1.0,
        'm' => 60.0,
        'h' => 3600.0,
    ];

    /**
     * Seconds for $raw, or null when it is not a duration. Null is a
     * parse refusal, not a zero: a caller distinguishes "absent" from
     * "immediately".
     */
    public static function parse(string $raw): ?float
    {
        $s = trim($raw);
        if ($s === '') {
            return null;
        }
        if (is_numeric($s)) {
            return (float) $s;
        }

        // Go's grammar is a sequence of value+unit pairs ("1m30s").
        if (preg_match_all('/(\d+(?:\.\d+)?)(ms|s|m|h)/', $s, $m, PREG_SET_ORDER) === 0) {
            return null;
        }
        // Reject trailing or interleaved junk: the matches must account
        // for the whole string, or "30x" would silently parse as 30s.
        $consumed = '';
        foreach ($m as $pair) {
            $consumed .= $pair[0];
        }
        if ($consumed !== $s) {
            return null;
        }

        $total = 0.0;
        foreach ($m as $pair) {
            $total += ((float) $pair[1]) * self::UNITS[$pair[2]];
        }
        return $total;
    }
}
