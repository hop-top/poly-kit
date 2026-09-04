<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * The `=?base64?...?=` header sentinel.
 *
 * Header values that must carry bytes outside the header charset are
 * wrapped in this sentinel. A value that merely looks like one is always
 * treated as one — there is no literal fallback — so a malformed payload
 * fails closed rather than being compared verbatim.
 *
 * Applied to `Mcp-Name` only; `Mcp-Method` and `MCP-Protocol-Version` are
 * compared as sent.
 */
final class Sentinel
{
    /**
     * Returns the decoded value, or null when the sentinel body is not
     * valid standard base64.
     *
     * A value without both markers is returned unchanged.
     */
    public static function decode(string $value): ?string
    {
        if (!str_starts_with($value, Protocol::SENTINEL_PREFIX)
            || !str_ends_with($value, Protocol::SENTINEL_SUFFIX)) {
            return $value;
        }

        $inner = substr(
            $value,
            \strlen(Protocol::SENTINEL_PREFIX),
            -\strlen(Protocol::SENTINEL_SUFFIX),
        );

        if ('' === $inner) {
            return '';
        }

        // Go decodes with StdEncoding, which requires canonical padding.
        // PHP's strict mode is laxer, so length and a round-trip are
        // checked too rather than trusting base64_decode alone.
        if (0 !== \strlen($inner) % 4) {
            return null;
        }

        $decoded = base64_decode($inner, true);

        if (false === $decoded || base64_encode($decoded) !== $inner) {
            return null;
        }

        return $decoded;
    }
}
