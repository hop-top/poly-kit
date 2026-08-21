<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * Cache hints stamped onto cacheable modern list results.
 *
 * Defaults are deliberately conservative: `ttlMs = 0` is honest, because
 * exposing or hiding a leaf can change the tool set at runtime and no
 * list-changed notification exists; `private` is the safe scope for a
 * tool list that may be caller-dependent.
 */
final readonly class CacheHints
{
    public const SCOPE_PRIVATE = 'private';
    public const SCOPE_PUBLIC = 'public';

    public function __construct(
        public int $ttlMs = 0,
        public string $cacheScope = self::SCOPE_PRIVATE,
    ) {
        if ($ttlMs < 0) {
            throw new \InvalidArgumentException('cmdsurface: WithMCPCacheHints: negative ttl');
        }

        if (!\in_array($cacheScope, [self::SCOPE_PRIVATE, self::SCOPE_PUBLIC], true)) {
            throw new \InvalidArgumentException(
                \sprintf('cmdsurface: WithMCPCacheHints: unrecognized scope %s', $cacheScope),
            );
        }
    }
}
