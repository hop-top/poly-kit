<?php

declare(strict_types=1);

namespace HopTop\Kit\Cli\Delegation;

/** Go `path.Match` globs and the policy's `verb:*` prefix form. */
final class Glob
{
    private function __construct()
    {
    }

    /**
     * `*` matches anything; others are `path.Match` globs; `verb:*` is a
     * prefix over the space-joined command path.
     *
     * @param list<string> $patterns
     */
    public static function matchAny(array $patterns, string $value): bool
    {
        foreach ($patterns as $p) {
            if ('*' === $p || true === self::match($p, $value)) {
                return true;
            }
            if (str_ends_with($p, ':*')) {
                $prefix = substr($p, 0, -2);
                if ($value === $prefix || str_starts_with($value, $prefix . ' ')) {
                    return true;
                }
            }
        }

        return false;
    }

    /** Go's `path.Match`; null for a malformed pattern. */
    public static function match(string $pattern, string $name): ?bool
    {
        $rx = self::regex($pattern);
        if (null === $rx) {
            return null;
        }

        return 1 === preg_match($rx, $name);
    }

    private static function regex(string $pattern): ?string
    {
        $pat = mb_str_split($pattern);
        $n = \count($pat);
        $out = '';
        $i = 0;
        while ($i < $n) {
            $c = $pat[$i];
            if ('*' === $c) {
                $out .= '[^/]*';
            } elseif ('?' === $c) {
                $out .= '[^/]';
            } elseif ('\\' === $c) {
                ++$i;
                if ($i >= $n) {
                    return null;
                }
                $out .= preg_quote($pat[$i], '~');
            } elseif ('[' === $c) {
                ++$i;
                $neg = $i < $n && '^' === $pat[$i];
                if ($neg) {
                    ++$i;
                }
                $items = '';
                $count = 0;
                while (true) {
                    if ($i < $n && ']' === $pat[$i] && $count > 0) {
                        break;
                    }
                    if ($i >= $n) {
                        return null;
                    }
                    if ('\\' === $pat[$i]) {
                        ++$i;
                        if ($i >= $n) {
                            return null;
                        }
                    }
                    $lo = $pat[$i++];
                    $hi = $lo;
                    if ($i < $n && '-' === $pat[$i]) {
                        ++$i;
                        if ($i < $n && '\\' === $pat[$i]) {
                            ++$i;
                        }
                        if ($i >= $n) {
                            return null;
                        }
                        $hi = $pat[$i++];
                    }
                    if (mb_ord($lo) > mb_ord($hi)) {
                        return null;
                    }
                    $items .= preg_quote($lo, '~') . '-' . preg_quote($hi, '~');
                    ++$count;
                }
                $out .= '[' . ($neg ? '^' : '') . $items . ']';
            } else {
                $out .= preg_quote($c, '~');
            }
            ++$i;
        }

        return '~\A' . $out . '\z~su';
    }
}
