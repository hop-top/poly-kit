<?php

declare(strict_types=1);

namespace HopTop\KitEngine;

/** Peer discovery (`/peers`). */
final class PeerClient
{
    public function __construct(private readonly Http $http)
    {
    }

    /** @return list<array<string,mixed>> */
    public function list(): array
    {
        /** @var list<array<string,mixed>> */
        return $this->http->request('GET', '/peers') ?? [];
    }

    public function trust(string $id): void
    {
        $this->http->request('POST', '/peers/' . rawurlencode($id) . '/trust');
    }

    public function block(string $id): void
    {
        $this->http->request('POST', '/peers/' . rawurlencode($id) . '/block');
    }
}
