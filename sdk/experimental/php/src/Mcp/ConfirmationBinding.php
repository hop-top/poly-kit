<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * The request context a `requestState` is bound to.
 *
 * Binding the token to the tool, its arguments and the caller is what
 * stops a confirmation minted for one call being replayed against a
 * different one.
 */
final readonly class ConfirmationBinding
{
    public function __construct(
        public string $tool,
        public string $argsDigest,
        public string $principal,
    ) {
    }

    /**
     * Builds the binding for one `tools/call`.
     *
     * @param array<string, mixed>|null $params
     */
    public static function forCall(Leaf $leaf, ?array $params, string $authorization): self
    {
        return new self(
            tool: $leaf->pathKey(),
            argsDigest: self::argsDigest($params),
            principal: '' === $authorization ? '' : hash('sha256', $authorization),
        );
    }

    /**
     * Digests the call arguments so a retry cannot swap them.
     *
     * Absent arguments digest as `null`, matching the Go encoder.
     *
     * @param array<string, mixed>|null $params
     */
    private static function argsDigest(?array $params): string
    {
        $arguments = $params['arguments'] ?? null;

        $canonical = \is_array($arguments) && [] !== $arguments
            ? Json::encode($arguments)
            : 'null';

        return hash('sha256', $canonical);
    }
}
