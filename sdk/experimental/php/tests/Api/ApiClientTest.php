<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Api;

use GuzzleHttp\Client;
use GuzzleHttp\Exception\BadResponseException;
use GuzzleHttp\Handler\MockHandler;
use GuzzleHttp\HandlerStack;
use GuzzleHttp\Middleware;
use GuzzleHttp\Psr7\Response;
use HopTop\Kit\Api\ApiClient;
use HopTop\Kit\Api\ApiException;
use PHPUnit\Framework\TestCase;

class ApiClientTest extends TestCase
{
    /** @var array<int, array<string, mixed>> */
    private array $history = [];

    private function makeClient(array $responses, ?string $token = null): ApiClient
    {
        $mock = new MockHandler($responses);
        $stack = HandlerStack::create($mock);
        $stack->push(Middleware::history($this->history));
        $http = new Client(['handler' => $stack]);

        return new ApiClient(
            baseURL: 'http://api.test',
            authToken: $token,
            httpClient: $http,
        );
    }

    private function lastRequest(): \Psr\Http\Message\RequestInterface
    {
        return $this->history[array_key_last($this->history)]['request'];
    }

    public function testCreateSendsPostWithJsonBody(): void
    {
        $client = $this->makeClient([
            new Response(201, ['Content-Type' => 'application/json'], '{"id":"1","name":"foo"}'),
        ]);

        $result = $client->create(['name' => 'foo']);

        $req = $this->lastRequest();
        $this->assertSame('POST', $req->getMethod());
        $this->assertSame('http://api.test/', (string) $req->getUri());
        $this->assertSame('{"name":"foo"}', (string) $req->getBody());
        $this->assertSame(['id' => '1', 'name' => 'foo'], $result);
    }

    public function testGetSendsGetWithId(): void
    {
        $client = $this->makeClient([
            new Response(200, ['Content-Type' => 'application/json'], '{"id":"42"}'),
        ]);

        $result = $client->get('42');

        $req = $this->lastRequest();
        $this->assertSame('GET', $req->getMethod());
        $this->assertSame('http://api.test/42', (string) $req->getUri());
        $this->assertSame(['id' => '42'], $result);
    }

    public function testListSendsGetWithQueryParams(): void
    {
        $client = $this->makeClient([
            new Response(200, ['Content-Type' => 'application/json'], '[{"id":"1"}]'),
        ]);

        $result = $client->list(['limit' => 10, 'search' => 'foo']);

        $req = $this->lastRequest();
        $this->assertSame('GET', $req->getMethod());
        $this->assertStringContainsString('limit=10', (string) $req->getUri());
        $this->assertStringContainsString('search=foo', (string) $req->getUri());
        $this->assertSame([['id' => '1']], $result);
    }

    public function testUpdateSendsPutWithIdAndJsonBody(): void
    {
        $client = $this->makeClient([
            new Response(200, ['Content-Type' => 'application/json'], '{"id":"5","name":"bar"}'),
        ]);

        $result = $client->update('5', ['id' => '5', 'name' => 'bar']);

        $req = $this->lastRequest();
        $this->assertSame('PUT', $req->getMethod());
        $this->assertSame('http://api.test/5', (string) $req->getUri());
        $this->assertSame('{"id":"5","name":"bar"}', (string) $req->getBody());
        $this->assertSame(['id' => '5', 'name' => 'bar'], $result);
    }

    public function testDeleteSendsDeleteWithId(): void
    {
        $client = $this->makeClient([
            new Response(204),
        ]);

        $client->delete('99');

        $req = $this->lastRequest();
        $this->assertSame('DELETE', $req->getMethod());
        $this->assertSame('http://api.test/99', (string) $req->getUri());
    }

    public function testNon2xxThrowsApiException(): void
    {
        $client = $this->makeClient([
            new Response(
                404,
                ['Content-Type' => 'application/json'],
                '{"status":404,"code":"not_found","message":"not found"}',
            ),
        ]);

        $this->expectException(ApiException::class);
        $this->expectExceptionMessage('not found');

        $client->get('missing');
    }

    public function testApiExceptionCarriesStatusAndCode(): void
    {
        $client = $this->makeClient([
            new Response(
                422,
                ['Content-Type' => 'application/json'],
                '{"status":422,"code":"validation_error","message":"bad input"}',
            ),
        ]);

        try {
            $client->create(['bad' => true]);
            $this->fail('Expected ApiException');
        } catch (ApiException $e) {
            $this->assertSame(422, $e->status);
            $this->assertSame('validation_error', $e->errorCode);
            $this->assertSame('bad input', $e->getMessage());
        }
    }

    public function testAuthTokenSetsAuthorizationHeader(): void
    {
        $client = $this->makeClient(
            [new Response(200, ['Content-Type' => 'application/json'], '[]')],
            token: 'secret-token',
        );

        $client->list();

        $req = $this->lastRequest();
        $this->assertSame('Bearer secret-token', $req->getHeaderLine('Authorization'));
    }

    public function testNoAuthTokenOmitsHeader(): void
    {
        $client = $this->makeClient([
            new Response(200, ['Content-Type' => 'application/json'], '[]'),
        ]);

        $client->list();

        $req = $this->lastRequest();
        $this->assertFalse($req->hasHeader('Authorization'));
    }

    public function testNon2xxWithoutJsonBodyThrowsGenericException(): void
    {
        $client = $this->makeClient([
            new Response(500, [], 'Internal Server Error'),
        ]);

        try {
            $client->get('1');
            $this->fail('Expected ApiException');
        } catch (ApiException $e) {
            $this->assertSame(500, $e->status);
            $this->assertSame('http_error', $e->errorCode);
        }
    }

    /**
     * @param array<int, Response|\Throwable> $responses
     */
    private function makeHttpsClient(array $responses, ?string $token = null): ApiClient
    {
        $mock = new MockHandler($responses);
        $stack = HandlerStack::create($mock);
        $stack->push(Middleware::history($this->history));
        $http = new Client(['handler' => $stack]);

        return new ApiClient(
            baseURL: 'https://api.test',
            authToken: $token,
            httpClient: $http,
        );
    }

    public function testRedirectToPlainHttpIsRefused(): void
    {
        // Downgrade attempt: https origin redirects to http. Following it would
        // put the Authorization header on the wire in cleartext.
        $client = $this->makeHttpsClient(
            [
                new Response(302, ['Location' => 'http://attacker.example/steal']),
                new Response(200, ['Content-Type' => 'application/json'], '{"id":"1"}'),
            ],
            token: 'secret-token',
        );

        try {
            $client->get('1');
            $this->fail('Expected the http downgrade redirect to be refused');
        } catch (BadResponseException $e) {
            $this->assertStringContainsString('allowed redirect protocols', $e->getMessage());
        }

        // Only the original request went out; nothing reached the attacker.
        $this->assertCount(1, $this->history);
        $this->assertSame('api.test', $this->lastRequest()->getUri()->getHost());
    }

    public function testCallerSuppliedOptionsCannotReenableHttpDowngrade(): void
    {
        // A caller re-enabling http redirects must not win over the hardening.
        $mock = new MockHandler([
            new Response(302, ['Location' => 'http://attacker.example/steal']),
            new Response(200, ['Content-Type' => 'application/json'], '{"id":"1"}'),
        ]);
        $stack = HandlerStack::create($mock);
        $stack->push(Middleware::history($this->history));
        $http = new Client([
            'handler' => $stack,
            // Client-level default deliberately permits the downgrade.
            'allow_redirects' => ['protocols' => ['http', 'https']],
        ]);
        $client = new ApiClient(
            baseURL: 'https://api.test',
            authToken: 'secret-token',
            httpClient: $http,
        );

        try {
            $client->get('1');
            $this->fail('Expected the http downgrade redirect to be refused');
        } catch (BadResponseException $e) {
            $this->assertStringContainsString('allowed redirect protocols', $e->getMessage());
        }

        $this->assertCount(1, $this->history);
        $this->assertSame('api.test', $this->lastRequest()->getUri()->getHost());
    }

    public function testHttpsRedirectStillFollowedWithinCap(): void
    {
        // Redirects remain legitimate for an API client as long as they stay
        // on https — only the downgrade is closed off.
        $client = $this->makeHttpsClient([
            new Response(302, ['Location' => 'https://api.test/moved']),
            new Response(200, ['Content-Type' => 'application/json'], '{"id":"1"}'),
        ]);

        $result = $client->get('1');

        $this->assertSame(['id' => '1'], $result);
        $this->assertCount(2, $this->history);
        $this->assertSame('https://api.test/moved', (string) $this->lastRequest()->getUri());
    }
}
