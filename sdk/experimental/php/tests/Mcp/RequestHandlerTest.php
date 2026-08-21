<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Mcp;

use HopTop\Kit\Mcp\Bridge;
use HopTop\Kit\Mcp\Mount;
use HopTop\Kit\Mcp\Policy;
use HopTop\Kit\Mcp\Protocol;
use HopTop\Kit\Mcp\RequestHandler;
use Nyholm\Psr7\Factory\Psr17Factory;
use Nyholm\Psr7\ServerRequest;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use Psr\Http\Server\RequestHandlerInterface;

/**
 * The PSR-15 binding.
 *
 * PHP has no serving infrastructure in this SDK, so the hosting layer is
 * built here rather than inherited. It stays a plain request handler so
 * adopters can mount it in any PSR-7 stack.
 */
final class RequestHandlerTest extends TestCase
{
    #[Test]
    public function itIsAStandardPsr15Handler(): void
    {
        self::assertInstanceOf(RequestHandlerInterface::class, $this->handler());
    }

    #[Test]
    public function itServesTheWireBytesUnalteredThroughPsr7(): void
    {
        $request = (new ServerRequest('POST', '/mcp'))
            ->withBody((new Psr17Factory())->createStream(
                '{"jsonrpc":"2.0","id":1,"method":"initialize"}',
            ));

        $response = $this->handler()->handle($request);

        self::assertSame(200, $response->getStatusCode());
        self::assertSame('application/json', $response->getHeaderLine('Content-Type'));
        self::assertSame(
            '{"jsonrpc":"2.0","id":1,"result":{"capabilities":{"tools":{}},'
            .'"protocolVersion":"2024-11-05","serverInfo":{"name":"cmdsurface","version":"0.0.0"}}}'."\n",
            (string) $response->getBody(),
            'the PSR-7 round-trip must not reserialize the body',
        );
    }

    #[Test]
    public function multiValueHeadersSurviveThePsr7Boundary(): void
    {
        // PSR-7 keeps every occurrence, which is what the duplicate-header
        // rules need; a comma-joined value would lose the distinction.
        $request = (new ServerRequest('POST', '/mcp'))
            ->withHeader(Protocol::HEADER_PROTOCOL_VERSION, ['2026-07-28', '2024-11-05'])
            ->withHeader(Protocol::HEADER_METHOD, 'tools/list')
            ->withBody((new Psr17Factory())->createStream(
                '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":'
                .'{"io.modelcontextprotocol/clientCapabilities":{},'
                .'"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}',
            ));

        $response = $this->handler()->handle($request);

        self::assertSame(400, $response->getStatusCode());
        self::assertStringContainsString('conflicting duplicate values', (string) $response->getBody());
    }

    #[Test]
    public function sessionVerbsAreRefusedByAPostSessionServer(): void
    {
        foreach (['GET', 'DELETE'] as $method) {
            $response = $this->handler()->handle(new ServerRequest($method, '/mcp'));

            self::assertSame(405, $response->getStatusCode(), $method.' must be refused');
            self::assertStringContainsString('method not allowed', (string) $response->getBody());
        }
    }

    #[Test]
    public function itReportsTheMountPathAdoptersShouldRouteTo(): void
    {
        self::assertSame('/mcp', $this->handler()->path());
    }

    private function handler(): RequestHandler
    {
        $factory = new Psr17Factory();

        return new RequestHandler(
            new Bridge(LockTrees::legacy(), Policy::default()),
            new Mount(),
            $factory,
            $factory,
        );
    }
}
