<?php

declare(strict_types=1);

namespace HopTop\Kit\Mcp;

/**
 * The reserved `params._meta` members a modern request carries.
 *
 * The 2026-07-28 era is stateless, so identity and capabilities travel on
 * every request instead of being negotiated once.
 */
final readonly class RequestMeta
{
    public function __construct(
        public string $version = '',
        public bool $versionIsText = false,
        public mixed $versionRaw = null,
        public bool $hasClientInfo = false,
        public string $clientName = '',
        public string $clientVersion = '',
    ) {
    }

    /**
     * V3: decodes `_meta`, requiring the two reserved keys.
     *
     * Returns a {@see CheckError} instead of throwing so the caller can
     * answer with the exact code and status the step mandates.
     *
     * @param array<string, mixed>|null $params
     */
    public static function parse(?array $params): self|CheckError
    {
        $fail = static fn (string $message): CheckError => new CheckError(
            ErrorCodes::INVALID_PARAMS,
            $message,
            400,
        );

        if (null === $params || [] === $params) {
            return $fail('missing required params._meta');
        }

        $meta = $params['_meta'] ?? null;

        if (!\is_array($meta)) {
            return $fail('missing required params._meta');
        }

        if (!\array_key_exists(Protocol::META_PROTOCOL_VERSION, $meta)) {
            return $fail('missing required _meta key: '.Protocol::META_PROTOCOL_VERSION);
        }

        if (!\array_key_exists(Protocol::META_CLIENT_CAPABILITIES, $meta)) {
            return $fail('missing required _meta key: '.Protocol::META_CLIENT_CAPABILITIES);
        }

        $rawVersion = $meta[Protocol::META_PROTOCOL_VERSION];
        $versionIsText = \is_string($rawVersion);

        // clientInfo only feeds audit metadata, so a value that is not an
        // object is treated as absent rather than rejected — V3 does not
        // require the key at all.
        $clientInfo = $meta[Protocol::META_CLIENT_INFO] ?? null;
        $hasClientInfo = \is_array($clientInfo);

        return new self(
            version: $versionIsText ? $rawVersion : '',
            versionIsText: $versionIsText,
            versionRaw: $rawVersion,
            hasClientInfo: $hasClientInfo,
            clientName: $hasClientInfo && \is_string($clientInfo['name'] ?? null) ? $clientInfo['name'] : '',
            clientVersion: $hasClientInfo && \is_string($clientInfo['version'] ?? null) ? $clientInfo['version'] : '',
        );
    }
}
