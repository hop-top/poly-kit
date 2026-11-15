<?php

declare(strict_types=1);

namespace HopTop\KitEngine;

use HopTop\KitEngine\Exception\HttpException;
use HopTop\KitEngine\Exception\VersionConflictException;
use Psr\Http\Client\ClientInterface;

/**
 * CRUD, history, revert and branch inspection for one document type.
 *
 * PHP has no generics: documents are `array<string,mixed>` envelopes
 * (`type`, `id`, `data`, `created_at`, `updated_at`). Pass `$validator`
 * to check outgoing documents (create/update); it receives the document
 * array and vetoes by throwing, before anything is sent.
 */
final class Collection
{
    private readonly Http $http;
    /** @var (callable(array<string,mixed>): void)|null */
    private $validator;

    /** @param (callable(array<string,mixed>): void)|null $validator */
    public function __construct(
        ClientInterface $client,
        string $baseUrl,
        private readonly string $type,
        ?string $token = null,
        ?callable $validator = null,
    ) {
        $this->http = new Http($client, $baseUrl, $token);
        $this->validator = $validator;
    }

    /**
     * @param array<string,mixed> $doc
     * @return array<string,mixed>
     */
    public function create(array $doc): array
    {
        $this->validate($doc);

        return $this->call('POST', '/', self::asBody($doc));
    }

    /** @return array<string,mixed> */
    public function get(string $id): array
    {
        return $this->call('GET', '/' . rawurlencode($id));
    }

    /** @return list<array<string,mixed>> */
    public function list(?CollectionQuery $query = null): array
    {
        /** @var list<array<string,mixed>> */
        return $this->http->request('GET', $this->path('/'), null, $query?->toQuery() ?? []) ?? [];
    }

    /**
     * Replaces the document body (PUT): `$data` is the whole new document,
     * sent verbatim; fields it omits are dropped, not kept.
     *
     * @param array<string,mixed> $data
     * @return array<string,mixed>
     */
    public function update(string $id, array $data): array
    {
        $this->validate($data);

        return $this->call('PUT', '/' . rawurlencode($id), self::asBody($data));
    }

    public function delete(string $id): void
    {
        $this->http->request('DELETE', $this->path('/' . rawurlencode($id)));
    }

    /**
     * Versions, newest first.
     *
     * @return list<array<string,mixed>>
     */
    public function history(string $id): array
    {
        $payload = $this->call('GET', '/' . rawurlencode($id) . '/history');

        return $payload['versions'] ?? [];
    }

    /**
     * Full `GET .../history?topology=1` payload: `heads` + DAG `versions`.
     *
     * @return array<string,mixed>
     */
    public function historyWithTopology(string $id): array
    {
        return $this->call('GET', '/' . rawurlencode($id) . '/history', null, ['topology' => 1]);
    }

    /**
     * Appends a new version restoring `$version`.
     *
     * @return array<string,mixed> document envelope at the reverted state
     * @throws VersionConflictException when `$version` does not exist (409)
     */
    public function revert(string $id, int $version): array
    {
        return $this->call(
            'POST',
            '/' . rawurlencode($id) . '/revert',
            ['version' => $version],
            [],
            static fn (int $status, string $method, string $path, string $body): ?HttpException => $status === 409
                ? new VersionConflictException($method, $path, $status, $body)
                : null,
        );
    }

    /**
     * Heads (tips) of the version DAG, most recent first.
     *
     * @return list<array<string,mixed>>
     */
    public function branches(string $id, bool $live = false): array
    {
        $payload = $this->call('GET', '/' . rawurlencode($id) . '/branches', null, $live ? ['live' => 1] : []);

        return $payload['heads'] ?? [];
    }

    /**
     * @param array<string,scalar> $query
     * @return array<string,mixed>
     */
    private function call(string $method, string $suffix, mixed $body = null, array $query = [], ?callable $mapError = null): array
    {
        $res = $this->http->request($method, $this->path($suffix), $body, $query, null, $mapError);

        /** @var array<string,mixed> */
        return \is_array($res) ? $res : [];
    }

    private function path(string $suffix): string
    {
        return '/' . rawurlencode($this->type) . $suffix;
    }

    /** @param array<string,mixed> $doc */
    private function validate(array $doc): void
    {
        if ($this->validator !== null) {
            ($this->validator)($doc);
        }
    }

    /**
     * @param array<string,mixed> $doc
     * @return array<string,mixed>|\stdClass an empty document must encode as `{}`
     */
    private static function asBody(array $doc): array|\stdClass
    {
        return $doc === [] ? new \stdClass() : $doc;
    }
}
