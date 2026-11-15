<?php

declare(strict_types=1);

namespace HopTop\KitEngine;

/** Sync remote management (`/sync/*`). */
final class SyncClient
{
    public function __construct(private readonly Http $http)
    {
    }

    /** @return array<string,mixed> */
    public function addRemote(string $name, string $url, string $mode = 'both', string $filter = ''): array
    {
        /** @var array<string,mixed> */
        return $this->http->request('POST', '/sync/remotes', [
            'name' => $name,
            'url' => $url,
            'mode' => $mode,
            'filter' => $filter,
        ]);
    }

    public function removeRemote(string $name): void
    {
        $this->http->request('DELETE', '/sync/remotes/' . rawurlencode($name));
    }

    /** @return list<array<string,mixed>> */
    public function status(): array
    {
        $payload = $this->http->request('GET', '/sync/status');

        return \is_array($payload) ? ($payload['remotes'] ?? []) : [];
    }
}
