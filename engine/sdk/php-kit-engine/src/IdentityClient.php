<?php

declare(strict_types=1);

namespace HopTop\KitEngine;

/** Node identity (`/identity`). */
final class IdentityClient
{
    public function __construct(private readonly Http $http)
    {
    }

    /**
     * Adds a camelCase `publicKey` alias beside `public_key` (ts parity).
     *
     * @return array<string,mixed>
     */
    public function publicKey(): array
    {
        /** @var array<string,mixed> $info */
        $info = $this->http->request('GET', '/identity') ?? [];
        $info['publicKey'] = $info['public_key'] ?? null;

        return $info;
    }

    /** @return array<string,mixed> e.g. ['valid' => true] */
    public function verify(string $data, string $signature): array
    {
        /** @var array<string,mixed> */
        return $this->http->request('POST', '/identity/verify', [
            'data' => $data,
            'signature' => $signature,
        ]);
    }
}
