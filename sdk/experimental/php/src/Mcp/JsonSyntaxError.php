<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * Reproduces Go's `encoding/json` syntax-error wording.
 *
 * The `-32700` response embeds the decoder's own message
 * (`"parse error: " + err.Error()`), so the text is part of the wire
 * contract rather than a diagnostic we are free to phrase ourselves.
 * PHP's `json_last_error_msg()` only ever says "Syntax error", so the
 * offending byte and Go's context phrase are recovered by scanning the
 * document the way Go's scanner does.
 *
 * Messages were captured from Go rather than inferred.
 */
final class JsonSyntaxError
{
    private const TRUNCATED = 'unexpected end of JSON input';

    /**
     * Returns the message Go's decoder would produce for $raw.
     *
     * Callers only reach this after PHP has already rejected the payload,
     * so a document that scans clean here still reports truncation — the
     * residual case Go words that way.
     */
    public static function describe(string $raw): string
    {
        $scanner = new self($raw);

        return $scanner->scan() ?? self::TRUNCATED;
    }

    private int $pos = 0;

    private function __construct(private readonly string $raw)
    {
    }

    /** Returns the error message, or null when the scan ran out of input. */
    private function scan(): ?string
    {
        $this->skipSpace();

        if ($this->pos >= \strlen($this->raw)) {
            return null;
        }

        $err = $this->scanValue();
        if (null !== $err) {
            return $err;
        }

        $this->skipSpace();

        if ($this->pos < \strlen($this->raw)) {
            return $this->invalid($this->raw[$this->pos], 'after top-level value');
        }

        return null;
    }

    /** Scans one value, returning an error message when malformed. */
    private function scanValue(): ?string
    {
        $this->skipSpace();

        if ($this->pos >= \strlen($this->raw)) {
            return null;
        }

        $char = $this->raw[$this->pos];

        if ('"' === $char) {
            $this->scanString();

            return null;
        }

        if ('-' === $char || ctype_digit($char)) {
            $this->scanNumber();

            return null;
        }

        return match (true) {
            '{' === $char => $this->scanObject(),
            '[' === $char => $this->scanArray(),
            't' === $char => $this->scanLiteral('true'),
            'f' === $char => $this->scanLiteral('false'),
            'n' === $char => $this->scanLiteral('null'),
            default => $this->invalid($char, 'looking for beginning of value'),
        };
    }

    private function scanObject(): ?string
    {
        ++$this->pos;
        $this->skipSpace();

        if ($this->pos >= \strlen($this->raw)) {
            return null;
        }

        if ('}' === $this->raw[$this->pos]) {
            ++$this->pos;

            return null;
        }

        while (true) {
            $this->skipSpace();

            if ($this->pos >= \strlen($this->raw)) {
                return null;
            }

            if ('"' !== $this->raw[$this->pos]) {
                return $this->invalid($this->raw[$this->pos], 'looking for beginning of object key string');
            }

            $this->scanString();

            $this->skipSpace();

            if ($this->pos >= \strlen($this->raw)) {
                return null;
            }

            if (':' !== $this->raw[$this->pos]) {
                return $this->invalid($this->raw[$this->pos], 'after object key');
            }
            ++$this->pos;

            $err = $this->scanValue();
            if (null !== $err) {
                return $err;
            }

            $this->skipSpace();

            if ($this->pos >= \strlen($this->raw)) {
                return null;
            }

            $char = $this->raw[$this->pos];

            if (',' === $char) {
                ++$this->pos;
                continue;
            }

            if ('}' === $char) {
                ++$this->pos;

                return null;
            }

            return $this->invalid($char, 'after object key:value pair');
        }
    }

    private function scanArray(): ?string
    {
        ++$this->pos;
        $this->skipSpace();

        if ($this->pos >= \strlen($this->raw)) {
            return null;
        }

        if (']' === $this->raw[$this->pos]) {
            ++$this->pos;

            return null;
        }

        while (true) {
            $err = $this->scanValue();
            if (null !== $err) {
                return $err;
            }

            $this->skipSpace();

            if ($this->pos >= \strlen($this->raw)) {
                return null;
            }

            $char = $this->raw[$this->pos];

            if (',' === $char) {
                ++$this->pos;
                continue;
            }

            if (']' === $char) {
                ++$this->pos;

                return null;
            }

            return $this->invalid($char, 'after array element');
        }
    }

    /**
     * Consumes a string token.
     *
     * A string cannot fail in a way Go reports against a specific byte —
     * an unterminated one is truncation — so this never yields a message.
     */
    private function scanString(): void
    {
        ++$this->pos;

        while ($this->pos < \strlen($this->raw)) {
            $char = $this->raw[$this->pos];

            if ('\\' === $char) {
                $this->pos += 2;
                continue;
            }

            if ('"' === $char) {
                ++$this->pos;

                return;
            }

            ++$this->pos;
        }
    }

    /**
     * Scans one of the bare literals, reporting the first byte that breaks
     * the expected spelling the way Go names it.
     */
    private function scanLiteral(string $literal): ?string
    {
        $length = \strlen($literal);

        for ($i = 0; $i < $length; ++$i) {
            // Go's Unmarshal scans a trailing sentinel space once the
            // buffer runs out, so a literal truncated at end-of-input is
            // reported against ' ' rather than as truncation. Verified
            // against Go: "tru" yields
            // "invalid character ' ' in literal true (expecting 'e')".
            $char = $this->pos + $i < \strlen($this->raw)
                ? $this->raw[$this->pos + $i]
                : ' ';
            if ($char === $literal[$i]) {
                continue;
            }

            return $this->invalid(
                $char,
                \sprintf("in literal %s (expecting '%s')", $literal, $literal[$i]),
            );
        }

        $this->pos += $length;

        return null;
    }

    /** Consumes a number token, which likewise cannot fail per-byte. */
    private function scanNumber(): void
    {
        if ('-' === $this->raw[$this->pos]) {
            ++$this->pos;
        }

        while ($this->pos < \strlen($this->raw)
            && (ctype_digit($this->raw[$this->pos])
                || \in_array($this->raw[$this->pos], ['.', 'e', 'E', '+', '-'], true))) {
            ++$this->pos;
        }
    }

    private function skipSpace(): void
    {
        while ($this->pos < \strlen($this->raw)
            && \in_array($this->raw[$this->pos], [" ", "\t", "\n", "\r"], true)) {
            ++$this->pos;
        }
    }

    /**
     * Formats one offending byte the way Go's `%q` verb renders a rune.
     */
    private function invalid(string $char, string $context): string
    {
        return \sprintf('invalid character %s %s', self::quoteRune($char), $context);
    }

    private static function quoteRune(string $char): string
    {
        return match ($char) {
            "'" => "'\\''",
            '\\' => "'\\\\'",
            "\n" => "'\\n'",
            "\t" => "'\\t'",
            "\r" => "'\\r'",
            default => "'".$char."'",
        };
    }
}
