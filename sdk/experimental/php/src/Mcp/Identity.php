<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * The caller a verifier established for one request.
 *
 * `caller` is the stable principal (a user id, a service account, a
 * client id), `tenant` the account it acts in, `scopes` what it was
 * granted. All may be empty: a verifier may accept a credential that
 * names no principal.
 *
 * The only source of identity is the mount's verifier. An
 * `Authorization` header is presence, not verification, and a caller or
 * scopes the request claims in its body never become identity. Mirrors
 * the Go surfaces, where only an established `Meta` admits a
 * `kit/auth-required` leaf.
 */
final readonly class Identity
{
    /**
     * @param list<string> $scopes
     */
    public function __construct(
        public string $caller = '',
        public string $tenant = '',
        public array $scopes = [],
    ) {
    }

    /**
     * Runs the mount's verifier over $request.
     *
     * Returns the identity it established, or null when there is no
     * verifier, it refused (returned null), returned anything that is not
     * an Identity, or threw: every failure fails closed.
     *
     * @param (\Closure(Request): ?Identity)|null $verifier
     */
    public static function establish(?\Closure $verifier, Request $request): ?self
    {
        if (null === $verifier) {
            return null;
        }

        try {
            $identity = $verifier($request);
        } catch (\Throwable) {
            return null;
        }

        return $identity instanceof self ? $identity : null;
    }
}
