<?php

declare(strict_types=1);

namespace HopTop\KitEngine\Tests;

use GuzzleHttp\Psr7\Response;
use HopTop\KitEngine\Collection;
use HopTop\KitEngine\CollectionQuery;
use HopTop\KitEngine\Exception\HttpException;
use HopTop\KitEngine\Exception\VersionConflictException;
use HopTop\KitEngine\KitEngine;
use HopTop\KitEngine\Tests\Support\RecordingClient;
use PHPUnit\Framework\TestCase;

final class CollectionTest extends TestCase
{
    private const DOC = [
        'type' => 'notes',
        'id' => 'abc',
        'data' => ['title' => 'Hello'],
        'created_at' => '2026-04-19T10:00:00Z',
        'updated_at' => '2026-04-19T10:05:00Z',
    ];

    private function make(?RecordingClient $client = null, ?string $token = 'tok', ?callable $validator = null): array
    {
        $client ??= new RecordingClient(static fn () => RecordingClient::json(200, self::DOC));

        return [new Collection($client, 'http://127.0.0.1:9090', 'notes', $token, $validator), $client];
    }

    public function testCreatePostsToTrailingSlashRoute(): void
    {
        [$c, $client] = $this->make(new RecordingClient(static fn () => RecordingClient::json(201, self::DOC)));

        self::assertSame(self::DOC, $c->create(['title' => 'Hello']));

        $r = $client->last();
        self::assertSame('POST', $r->getMethod());
        self::assertSame('http://127.0.0.1:9090/notes/', (string) $r->getUri());
        self::assertSame('application/json', $r->getHeaderLine('Content-Type'));
        self::assertSame('{"title":"Hello"}', (string) $r->getBody());
    }

    public function testCreateEmptyDocEncodesAsObject(): void
    {
        [$c, $client] = $this->make();
        $c->create([]);

        self::assertSame('{}', (string) $client->last()->getBody());
    }

    public function testGetEncodesId(): void
    {
        [$c, $client] = $this->make();
        self::assertSame(self::DOC, $c->get('a/b c'));

        $r = $client->last();
        self::assertSame('GET', $r->getMethod());
        self::assertSame('/notes/a%2Fb%20c', $r->getUri()->getPath());
        self::assertSame('', (string) $r->getBody());
    }

    public function testListWithoutQueryHasNoQueryString(): void
    {
        [$c, $client] = $this->make(new RecordingClient(static fn () => RecordingClient::json(200, [self::DOC])));

        self::assertSame([self::DOC], $c->list());
        self::assertSame('http://127.0.0.1:9090/notes/', (string) $client->last()->getUri());
    }

    public function testListSerializesQuery(): void
    {
        [$c, $client] = $this->make(new RecordingClient(static fn () => RecordingClient::json(200, [])));

        $c->list(new CollectionQuery(limit: 10, offset: 0, sort: 'updated_at', search: 'hello world'));

        parse_str($client->last()->getUri()->getQuery(), $q);
        self::assertSame(['limit' => '10', 'offset' => '0', 'sort' => 'updated_at', 'search' => 'hello world'], $q);
        self::assertSame('/notes/', $client->last()->getUri()->getPath());
    }

    public function testListOmitsUnsetQueryFields(): void
    {
        [$c, $client] = $this->make(new RecordingClient(static fn () => RecordingClient::json(200, [])));

        $c->list(new CollectionQuery(limit: 5));

        self::assertSame('limit=5', $client->last()->getUri()->getQuery());
    }

    public function testUpdatePutsBody(): void
    {
        [$c, $client] = $this->make();
        $c->update('abc', ['title' => 'Updated']);

        $r = $client->last();
        self::assertSame('PUT', $r->getMethod());
        self::assertSame('/notes/abc', $r->getUri()->getPath());
        self::assertSame('{"title":"Updated"}', (string) $r->getBody());
    }

    public function testDeleteHandles204(): void
    {
        [$c, $client] = $this->make(new RecordingClient(static fn () => new Response(204)));
        $c->delete('abc');

        self::assertSame('DELETE', $client->last()->getMethod());
        self::assertSame('/notes/abc', $client->last()->getUri()->getPath());
    }

    public function testHistoryUnwrapsVersions(): void
    {
        $versions = [['version' => 2, 'data' => [], 'timestamp' => 't', 'operation' => 'update']];
        [$c, $client] = $this->make(new RecordingClient(static fn () => RecordingClient::json(200, ['versions' => $versions])));

        self::assertSame($versions, $c->history('abc'));
        self::assertSame('GET', $client->last()->getMethod());
        self::assertSame('http://127.0.0.1:9090/notes/abc/history', (string) $client->last()->getUri());
    }

    public function testHistoryWithTopologyReturnsWholePayload(): void
    {
        $payload = ['heads' => ['v1'], 'versions' => [['version_id' => 'v1', 'seq' => 1, 'parent_ids' => [], 'timestamp' => 't']]];
        [$c, $client] = $this->make(new RecordingClient(static fn () => RecordingClient::json(200, $payload)));

        self::assertSame($payload, $c->historyWithTopology('abc'));
        self::assertSame('http://127.0.0.1:9090/notes/abc/history?topology=1', (string) $client->last()->getUri());
    }

    public function testRevertPostsVersionBody(): void
    {
        [$c, $client] = $this->make();
        self::assertSame(self::DOC, $c->revert('abc', 2));

        $r = $client->last();
        self::assertSame('POST', $r->getMethod());
        self::assertSame('/notes/abc/revert', $r->getUri()->getPath());
        self::assertSame('{"version":2}', (string) $r->getBody());
    }

    public function testRevertOfMissingVersionThrowsVersionConflict(): void
    {
        [$c] = $this->make(new RecordingClient(static fn () => RecordingClient::json(409, ['status' => 409, 'code' => 'conflict'])));

        try {
            $c->revert('abc', 99);
            self::fail('expected VersionConflictException');
        } catch (VersionConflictException $e) {
            self::assertSame(409, $e->status);
            self::assertInstanceOf(HttpException::class, $e);
        }
    }

    public function testOtherErrorsAreHttpExceptionNotConflict(): void
    {
        [$c] = $this->make(new RecordingClient(static fn () => RecordingClient::json(404, ['status' => 404])));

        try {
            $c->revert('abc', 1);
            self::fail('expected HttpException');
        } catch (HttpException $e) {
            self::assertNotInstanceOf(VersionConflictException::class, $e);
            self::assertSame(404, $e->status);
        }
    }

    public function testGet404IsHttpException(): void
    {
        [$c] = $this->make(new RecordingClient(static fn () => RecordingClient::json(404, [])));
        $this->expectException(HttpException::class);
        $c->get('missing');
    }

    public function testBranchesUnwrapsHeads(): void
    {
        $heads = [['version_id' => 'v1', 'seq' => 4, 'parent_ids' => [], 'timestamp' => 't']];
        [$c, $client] = $this->make(new RecordingClient(static fn () => RecordingClient::json(200, ['heads' => $heads])));

        self::assertSame($heads, $c->branches('abc'));
        self::assertSame('http://127.0.0.1:9090/notes/abc/branches', (string) $client->last()->getUri());

        $c->branches('abc', true);
        self::assertSame('http://127.0.0.1:9090/notes/abc/branches?live=1', (string) $client->last()->getUri());
    }

    public function testBearerOnEveryRequestWhenTokenPresent(): void
    {
        [$c, $client] = $this->make(new RecordingClient(static fn () => RecordingClient::json(200, ['versions' => [], 'heads' => []])));
        $c->get('a');
        $c->list();
        $c->create(['a' => 1]);
        $c->update('a', ['a' => 2]);
        $c->history('a');
        $c->historyWithTopology('a');
        $c->revert('a', 1);
        $c->branches('a');

        self::assertCount(8, $client->requests);
        foreach ($client->requests as $r) {
            self::assertSame('Bearer tok', $r->getHeaderLine('Authorization'), $r->getMethod() . ' ' . $r->getUri());
        }
    }

    public function testNoAuthorizationHeaderWithoutToken(): void
    {
        [$c, $client] = $this->make(null, null);
        $c->create(['a' => 1]);
        $c->get('a');

        foreach ($client->requests as $r) {
            self::assertFalse($r->hasHeader('Authorization'));
        }
    }

    public function testValidatorRunsBeforeWritesAndCanVeto(): void
    {
        $seen = [];
        [$c, $client] = $this->make(null, 'tok', function (array $doc) use (&$seen): void {
            $seen[] = $doc;
            if (!isset($doc['title'])) {
                throw new \InvalidArgumentException('title required');
            }
        });

        $c->create(['title' => 'x']);
        $c->update('abc', ['title' => 'y']);
        self::assertSame([['title' => 'x'], ['title' => 'y']], $seen);

        $n = \count($client->requests);
        try {
            $c->create(['body' => 'no title']);
            self::fail('expected veto');
        } catch (\InvalidArgumentException) {
            self::assertCount($n, $client->requests, 'vetoed write never hits the wire');
        }
    }

    public function testEngineAccessorMemoizesPerType(): void
    {
        $engine = KitEngine::connect(9090, 't', null, new RecordingClient(static fn () => RecordingClient::json(200, ['pid' => 1])));

        self::assertSame($engine->collection('notes'), $engine->collection('notes'));
        self::assertNotSame($engine->collection('notes'), $engine->collection('tasks'));
    }

    public function testEngineCollectionUsesEngineTokenAndBase(): void
    {
        $client = new RecordingClient(static fn () => RecordingClient::json(200, ['pid' => 1]));
        $engine = KitEngine::connect(9091, 'tok', null, $client);
        $engine->collection('notes')->get('x');

        self::assertSame('http://127.0.0.1:9091/notes/x', (string) $client->last()->getUri());
        self::assertSame('Bearer tok', $client->last()->getHeaderLine('Authorization'));
    }
}
