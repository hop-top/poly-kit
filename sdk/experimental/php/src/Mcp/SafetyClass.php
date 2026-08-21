<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * The bridge's read of a leaf's safety annotations.
 *
 * Ports Go's `SafetyClass`. It is the input the policy gate consults to
 * decide whether a given {@see Surface} may invoke the leaf.
 */
final readonly class SafetyClass
{
    /**
     * @param bool          $destructive          true when kit/side-effect is one of the destructive tiers
     * @param bool          $authRequired         true when kit/auth-required is "true"
     * @param bool          $requiresConfirmation true when kit/requires-confirmation is "true"
     * @param list<string>  $permissions          parsed kit/permissions (comma-separated scopes)
     * @param list<string>  $exitCodes            parsed kit/exit-codes (comma-separated symbols)
     */
    public function __construct(
        public bool $destructive = false,
        public bool $authRequired = false,
        public bool $requiresConfirmation = false,
        public array $permissions = [],
        public array $exitCodes = [],
    ) {
    }

    /**
     * Reads cobra-style annotations and returns the bridge-side class.
     *
     * An empty annotation map yields a zero-value class — a read-only,
     * no-auth command — matching Go's nil-cmd/nil-Annotations behaviour.
     *
     * @param array<string, string> $annotations
     */
    public static function classify(array $annotations): self
    {
        $sideEffect = $annotations['kit/side-effect'] ?? '';

        return new self(
            destructive: \in_array($sideEffect, ['destructive', 'destructive-local', 'destructive-shared'], true),
            authRequired: ($annotations['kit/auth-required'] ?? '') === 'true',
            requiresConfirmation: ($annotations['kit/requires-confirmation'] ?? '') === 'true',
            permissions: self::splitCsv($annotations['kit/permissions'] ?? ''),
            exitCodes: self::splitCsv($annotations['kit/exit-codes'] ?? ''),
        );
    }

    /**
     * Parses a comma-separated annotation value, trimming whitespace and
     * dropping empty entries.
     *
     * @return list<string>
     */
    private static function splitCsv(string $value): array
    {
        if ('' === $value) {
            return [];
        }

        $out = [];
        foreach (explode(',', $value) as $part) {
            $part = trim($part);
            if ('' !== $part) {
                $out[] = $part;
            }
        }

        return $out;
    }
}
