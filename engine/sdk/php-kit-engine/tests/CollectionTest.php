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

    private const HEAD = ['version_id' => 'v2', 'seq' => 4, 'parent_ids' => ['v1'], 'timestamp' => 't'];

    public function testForkPostsFromSeqAndReturnsHead(): void
    {
        [$c, $client] = $this->make(new RecordingClient(static fn () => RecordingClient::json(201, self::HEAD)));

        self::assertSame(self::HEAD, $c->fork('a b', 2));

        $r = $client->last();
        self::assertSame('POST', $r->getMethod());
        self::assertSame('/notes/a%20b/fork', $r->getUri()->getPath());
        self::assertSame('{"from_seq":2}', (string) $r->getBody());
    }

    public function testMergePostsSeqsAndData(): void
    {
        [$c, $client] = $this->make(new RecordingClient(static fn () => RecordingClient::json(201, self::HEAD)));

        self::assertSame(self::HEAD, $c->merge('abc', 4, 3, ['title' => 'merged']));

        $r = $client->last();
        self::assertSame('POST', $r->getMethod());
        self::assertSame('/notes/abc/merge', $r->getUri()->getPath());
        self::assertSame('{"source_seq":4,"target_seq":3,"data":{"title":"merged"}}', (string) $r->getBody());
    }

    public function testMergeEmptyDataEncodesAsObject(): void
    {
        [$c, $client] = $this->make(new RecordingClient(static fn () => RecordingClient::json(201, self::HEAD)));
        $c->merge('abc', 2, 1, []);

        self::assertSame('{"source_seq":2,"target_seq":1,"data":{}}', (string) $client->last()->getBody());
    }

    public function testPruneSendsOnlySetPolicyFields(): void
    {
        $result = ['versions_removed' => ['v1'], 'blobs_freed' => 1, 'bytes_freed' => 9];
        [$c, $client] = $this->make(new RecordingClient(static fn () => RecordingClient::json(200, $result)));

        self::assertSame($result, $c->prune('abc', maxVersions: 10));
        self::assertSame('POST', $client->last()->getMethod());
        self::assertSame('/notes/abc/prune', $client->last()->getUri()->getPath());
        self::assertSame('{"max_versions":10}', (string) $client->last()->getBody());

        $c->prune('abc', maxVersions: 10, maxAgeSeconds: 60);
        self::assertSame('{"max_versions":10,"max_age_seconds":60}', (string) $client->last()->getBody());

        $c->prune('abc', maxAgeSeconds: 60);
        self::assertSame('{"max_age_seconds":60}', (string) $client->last()->getBody());
    }

    public function testPruneWithoutPolicyEncodesEmptyObject(): void
    {
        [$c, $client] = $this->make(new RecordingClient(static fn () => RecordingClient::json(400, [])));

        try {
            $c->prune('abc');
            self::fail('expected HttpException');
        } catch (HttpException $e) {
            self::assertSame(400, $e->status);
        }
        self::assertSame('{}', (string) $client->last()->getBody());
    }

    public function testAbandonPostsSeqAndAcceptsEmptyBody(): void
    {
        [$c, $client] = $this->make(new RecordingClient(static fn () => new Response(200)));
        $c->abandon('abc', 6);

        $r = $client->last();
        self::assertSame('POST', $r->getMethod());
        self::assertSame('/notes/abc/abandon', $r->getUri()->getPath());
        self::assertSame('{"seq":6}', (string) $r->getBody());
    }

    /** @return array<string, array{string}> */
    public static function branchOps(): array
    {
        return ['fork' => ['fork'], 'merge' => ['merge'], 'abandon' => ['abandon']];
    }

    #[\PHPUnit\Framework\Attributes\DataProvider('branchOps')]
    public function testBranchOpsMap409ToVersionConflictAnd404ToHttpException(string $op): void
    {
        $call = fn (Collection $c) => match ($op) {
            'fork' => $c->fork('abc', 99),
            'merge' => $c->merge('abc', 99, 1, ['a' => 1]),
            'abandon' => $c->abandon('abc', 99),
        };

        [$c] = $this->make(new RecordingClient(static fn () => RecordingClient::json(409, ['status' => 409])));
        try {
            $call($c);
            self::fail('expected VersionConflictException');
        } catch (VersionConflictException $e) {
            self::assertSame(409, $e->status);
        }

        [$c] = $this->make(new RecordingClient(static fn () => RecordingClient::json(404, ['status' => 404])));
        try {
            $call($c);
            self::fail('expected HttpException');
        } catch (HttpException $e) {
            self::assertNotInstanceOf(VersionConflictException::class, $e);
            self::assertSame(404, $e->status);
        }
    }

    public function testPrune404IsHttpException(): void
    {
        [$c] = $this->make(new RecordingClient(static fn () => RecordingClient::json(404, [])));
        $this->expectException(HttpException::class);
        $c->prune('missing', maxVersions: 1);
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
        $c->fork('a', 1);
        $c->merge('a', 2, 1, ['a' => 1]);
        $c->prune('a', maxVersions: 1);
        $c->abandon('a', 2);

        self::assertCount(12, $client->requests);
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
