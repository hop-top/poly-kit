<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * Mints and verifies the opaque `requestState` an MRTR retry echoes back.
 *
 * The surface stays stateless: everything a retry needs to be trusted is
 * carried inside the token and re-derived on arrival, so no server-side
 * pending-confirmation table exists to leak or expire.
 *
 * Wire format is `v1.<expiry-unix>.<base64url-unpadded(hmac)>`; only the
 * version and expiry are readable, and neither is trusted before the MAC
 * verifies.
 */
final readonly class ConfirmationState
{
    public const VERSION = 'v1';
    public const TTL_SECONDS = 300;

    public function __construct(private string $key)
    {
        if ('' === $key) {
            throw new \InvalidArgumentException('cmdsurface: WithMCPConfirmationKey: empty key');
        }
    }

    public function mint(ConfirmationBinding $binding, int $expiresAt): string
    {
        return self::VERSION.'.'.$expiresAt.'.'.self::base64UrlEncode($this->mac($binding, $expiresAt));
    }

    /**
     * Verifies a presented token against the current request's binding.
     *
     * Authenticity is checked before expiry: a forged token must be
     * reported as invalid rather than merely stale, so a tampered expiry
     * cannot downgrade the outcome.
     */
    public function verify(string $state, ConfirmationBinding $binding, int $now): ConfirmationStatus
    {
        $parts = explode('.', $state);

        if (3 !== \count($parts) || self::VERSION !== $parts[0]) {
            return ConfirmationStatus::Invalid;
        }

        if (!preg_match('/^-?\d+$/', $parts[1])) {
            return ConfirmationStatus::Invalid;
        }

        $expiresAt = (int) $parts[1];
        $tag = self::base64UrlDecode($parts[2]);

        if (null === $tag || !hash_equals($this->mac($binding, $expiresAt), $tag)) {
            return ConfirmationStatus::Invalid;
        }

        return $expiresAt < $now ? ConfirmationStatus::Expired : ConfirmationStatus::Valid;
    }

    /**
     * Length-prefixes every part before signing so that no rearrangement
     * of the fields can produce the same input.
     */
    private function mac(ConfirmationBinding $binding, int $expiresAt): string
    {
        $parts = [
            'cmdsurface-mcp-confirm-'.self::VERSION,
            (string) $expiresAt,
            $binding->tool,
            $binding->argsDigest,
            $binding->principal,
        ];

        $payload = '';
        foreach ($parts as $part) {
            $payload .= \strlen($part).':'.$part;
        }

        return hash_hmac('sha256', $payload, $this->key, true);
    }

    private static function base64UrlEncode(string $raw): string
    {
        return rtrim(strtr(base64_encode($raw), '+/', '-_'), '=');
    }

    private static function base64UrlDecode(string $encoded): ?string
    {
        $decoded = base64_decode(strtr($encoded, '-_', '+/'), true);

        return false === $decoded ? null : $decoded;
    }
}
