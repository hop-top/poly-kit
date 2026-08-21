<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * Byte-exact JSON encoding matching Go's `encoding/json`.
 *
 * Three behaviours have to line up for the cross-language wire fixtures
 * to compare equal as bytes, with no decode/re-encode step:
 *
 *  - **Key order.** Go marshals a `map[string]any` with lexicographically
 *    sorted keys. PHP preserves insertion order instead, so keys are
 *    sorted explicitly here rather than left to `json_encode`.
 *  - **Slashes.** PHP escapes `/` as `\/` by default; Go does not. Hence
 *    `JSON_UNESCAPED_SLASHES` — load-bearing for the reserved
 *    `io.modelcontextprotocol/*` `_meta` keys.
 *  - **Trailing newline.** Go's `json.Encoder.Encode` appends `\n`; the
 *    fixtures include it, so {@see self::encodeLine()} does too.
 *
 * Empty JSON objects are represented as {@see \stdClass} (`{}`), which is
 * how PHP distinguishes them from empty arrays (`[]`).
 */
final class Json
{
    /**
     * Go's `json.Encoder.Encode`: sorted keys, unescaped slashes, and a
     * trailing newline.
     */
    public static function encodeLine(mixed $value): string
    {
        return self::encode($value)."\n";
    }

    /**
     * Encodes a JSON-RPC envelope the way Go marshals `jsonRPCResponse`.
     *
     * The envelope is a Go *struct*, so its members keep declaration order
     * (`jsonrpc`, `id`, then `result` or `error`) instead of being sorted;
     * only the nested result/error payloads are maps and therefore sorted.
     * Mixing the two rules up reorders every response and fails the wire
     * fixtures, so the envelope is assembled here rather than by the
     * generic sorter.
     *
     * $id is passed pre-decoded and is emitted verbatim, including null.
     *
     * @param array<string, mixed>|null $error
     */
    public static function encodeEnvelope(
        mixed $id,
        mixed $result = null,
        ?array $error = null,
        bool $omitId = false,
    ): string {
        $parts = ['"jsonrpc":"2.0"'];

        if (!$omitId) {
            $parts[] = '"id":'.self::encode($id);
        }

        if (null !== $error) {
            $parts[] = '"error":'.self::encodeError($error);
        } else {
            $parts[] = '"result":'.self::encode($result);
        }

        return '{'.implode(',', $parts)."}\n";
    }

    /**
     * Encodes a JSON-RPC error object in Go's struct order: `code`,
     * `message`, then the optional `data` payload.
     *
     * @param array<string, mixed> $error
     */
    private static function encodeError(array $error): string
    {
        $parts = [
            '"code":'.self::encode($error['code']),
            '"message":'.self::encode($error['message']),
        ];

        if (\array_key_exists('data', $error) && null !== $error['data']) {
            $parts[] = '"data":'.self::encode($error['data']);
        }

        return '{'.implode(',', $parts).'}';
    }

    /**
     * Go's `json.Marshal`: sorted keys and unescaped slashes, no newline.
     */
    public static function encode(mixed $value): string
    {
        $encoded = json_encode(
            self::sortKeys($value),
            \JSON_UNESCAPED_SLASHES | \JSON_UNESCAPED_UNICODE | \JSON_THROW_ON_ERROR,
        );

        return self::escapeLikeGo($encoded);
    }

    /**
     * Applies Go's extra escapes on top of PHP's output.
     *
     * `encoding/json` HTML-escapes `<`, `>` and `&` by default, and escapes
     * the U+2028/U+2029 line separators, while leaving `/` and the rest of
     * UTF-8 raw. PHP's flags cover the slash and Unicode halves; these four
     * replacements close the gap. Verified against Go rather than assumed.
     *
     * Operating on encoded output is safe: each needle is a literal byte
     * sequence that cannot occur inside an already-escaped form.
     */
    private static function escapeLikeGo(string $encoded): string
    {
        return strtr($encoded, [
            '<' => '\\u003c',
            '>' => '\\u003e',
            '&' => '\\u0026',
            "\u{2028}" => '\\u2028',
            "\u{2029}" => '\\u2029',
        ]);
    }

    /**
     * Recursively sorts object keys so PHP's insertion order matches Go's
     * lexicographic map ordering.
     *
     * List-shaped arrays keep their order — element order is semantic, and
     * Go does not reorder slices.
     */
    private static function sortKeys(mixed $value): mixed
    {
        if ($value instanceof \stdClass) {
            $sorted = (array) $value;
            ksort($sorted, \SORT_STRING);

            $out = new \stdClass();
            foreach ($sorted as $key => $item) {
                $out->{$key} = self::sortKeys($item);
            }

            return $out;
        }

        if (!\is_array($value)) {
            return $value;
        }

        if (array_is_list($value)) {
            return array_map(self::sortKeys(...), $value);
        }

        ksort($value, \SORT_STRING);

        return array_map(self::sortKeys(...), $value);
    }

    /**
     * Decodes a JSON document, preserving objects as associative arrays.
     *
     * Returns null when the payload is not valid JSON; callers map that to
     * the `-32700` parse-error response.
     */
    public static function decode(string $raw): mixed
    {
        try {
            return json_decode($raw, true, 512, \JSON_THROW_ON_ERROR);
        } catch (\JsonException) {
            return null;
        }
    }
}
