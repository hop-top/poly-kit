<?php

declare(strict_types=1);

namespace HopTop\Kit\Telemetry;

use Closure;

/**
 * Best-effort PII / token-prefix redactor for telemetry envelopes.
 *
 * Redaction is intentionally best-effort:
 *   * The default pattern set catches the obvious shapes — email,
 *     IPv4 / IPv6, $HOME prefix, common token prefixes
 *     (`sk-`, `gh[pousr]_`, `xoxb-`).
 *   * Adopters can supply a custom callback for project-specific
 *     redaction. The callback runs AFTER the default pass; we then
 *     re-run the default pass on its output as defense in depth
 *     (a custom rule may extract a substring that still contains
 *     a default-matching token).
 *
 * Nothing here promises perfect coverage. The contract is: never throw,
 * never leak the obvious shapes, never widen the attribute set.
 */
final class Redactor
{
    /**
     * Pattern => replacement. Ordered: token prefixes before generic
     * IPv6 to keep specific matches authoritative.
     */
    private const REPLACEMENTS = [
        '/\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b/' => '<redacted:email>',
        '/\bsk-[A-Za-z0-9_-]{8,}\b/' => '<redacted:token>',
        '/\bgh[pousr]_[A-Za-z0-9_-]{16,}\b/' => '<redacted:token>',
        '/\bxoxb-[0-9]+-[0-9]+-[A-Za-z0-9]{24,}\b/' => '<redacted:token>',
        '/\b(?:\d{1,3}\.){3}\d{1,3}\b/' => '<redacted:ipv4>',
        // IPv6: matches full and compressed (::) forms. Intentionally
        // permissive — telemetry redaction errs toward false positives.
        '/(?<![0-9a-fA-F:])(?:(?:[0-9a-fA-F]{1,4}:){1,7}(?::[0-9a-fA-F]{1,4})+|(?:[0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4})(?![0-9a-fA-F:])/' => '<redacted:ipv6>',
    ];

    private ?Closure $customRedactor = null;

    /**
     * @param callable(mixed):mixed|null $custom Optional project-specific
     *        redactor. Receives the value AFTER default redaction; its
     *        output is then re-run through default redaction.
     */
    public function __construct(?callable $custom = null)
    {
        if ($custom !== null) {
            $this->customRedactor = Closure::fromCallable($custom);
        }
    }

    /**
     * Recursively redact a value. Strings are pattern-matched; arrays
     * are walked element-wise; scalars (int / float / bool / null) and
     * objects are returned unchanged.
     */
    public function redact(mixed $value): mixed
    {
        if (is_string($value)) {
            return $this->redactString($value);
        }

        if (is_array($value)) {
            $out = [];
            foreach ($value as $k => $v) {
                $out[$k] = $this->redact($v);
            }

            return $out;
        }

        return $value;
    }

    /**
     * Convenience for envelope attribute maps: run default redaction,
     * then the custom callback if one was provided, then default
     * redaction again on the callback output.
     *
     * @param array<string, mixed> $attrs
     * @return array<string, mixed>
     */
    public function redactAttrs(array $attrs): array
    {
        $r = $this->redact($attrs);

        if ($this->customRedactor !== null) {
            $r = ($this->customRedactor)($r);
            // Defense in depth: a custom rule could (e.g.) reformat a
            // string into a shape that still contains a default match.
            $r = $this->redact($r);
        }

        if (!is_array($r)) {
            // A misbehaving custom callback could swap the type; coerce
            // back to a shape the caller expects.
            return [];
        }

        /** @var array<string, mixed> $r */
        return $r;
    }

    private function redactString(string $value): string
    {
        $out = $value;
        foreach (self::REPLACEMENTS as $pattern => $replacement) {
            $replaced = preg_replace($pattern, $replacement, $out);
            if ($replaced !== null) {
                $out = $replaced;
            }
        }

        $home = (string) ($_SERVER['HOME'] ?? getenv('HOME') ?: '');
        if ($home !== '' && $home !== '/') {
            $out = str_replace($home, '$HOME', $out);
        }

        return $out;
    }
}
