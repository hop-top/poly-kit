<?php

declare(strict_types=1);

namespace HopTop\Kit\Tests\Mcp;

use HopTop\Kit\Mcp\Bridge;
use HopTop\Kit\Mcp\Command;
use HopTop\Kit\Mcp\Identity;
use HopTop\Kit\Mcp\Mount;
use HopTop\Kit\Mcp\Policy;
use HopTop\Kit\Mcp\Request;
use HopTop\Kit\Mcp\RequestHandler;
use HopTop\Kit\Mcp\Result;
use HopTop\Kit\Mcp\ServerInfo;
use HopTop\Kit\Mcp\SpecVersion;
use HopTop\Kit\Mcp\StdioServer;
use Nyholm\Psr7\Factory\Psr17Factory;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;

/**
 * The stdio binding: newline framing, notifications never answered,
 * ping, parse / batch refusals, sequential replies, both eras with no
 * header layer, fail-closed gates, and a clean return at end of input.
 */
final class StdioServerTest extends TestCase
{
    private const MODERN_META = '"_meta":{"io.modelcontextprotocol/clientCapabilities":{},'
        .'"io.modelcontextprotocol/protocolVersion":"2026-07-28"}';

    /** What the last {@see self::serveLines()} wrote to the log stream. */
    private string $log = '';

    #[Test]
    public function itRunsTheLegacyHandshakeAndNeverAnswersANotification(): void
    {
        $lines = $this->serveLines(
            self::bridge(),
            null,
            '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05",'
            .'"capabilities":{},"clientInfo":{"name":"pipe","version":"0"}}}',
            '{"jsonrpc":"2.0","method":"notifications/initialized"}',
            '{"jsonrpc":"2.0","id":2,"method":"tools/list"}',
            '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ping"}}',
        );

        self::assertCount(3, $lines);
        self::assertSame([1, 2, 3], array_map(static fn (array $m): mixed => $m['id'], self::decodeAll($lines)));
        self::assertStringContainsString('"protocolVersion":"2024-11-05"', $lines[0]);
        self::assertStringContainsString('pong', $lines[2]);
    }

    #[Test]
    public function itAnswersPingWithAnEmptyResult(): void
    {
        self::assertSame(
            ['{"jsonrpc":"2.0","id":"p1","result":{}}'],
            $this->serveLines(self::bridge(), null, '{"jsonrpc":"2.0","id":"p1","method":"ping"}'),
        );
    }

    #[Test]
    public function itDropsEveryNotificationEvenAMalformedOrFailingOne(): void
    {
        self::assertSame([], $this->serveLines(
            self::bridge(),
            null,
            '{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}',
            '{"jsonrpc":"1.0","method":"notifications/initialized"}',
            '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"ping"}}',
            '{"jsonrpc":"2.0","method":"nope"}',
            '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"boom"}}',
            '{"jsonrpc":"2.0","method":"tools/list","params":{'.self::MODERN_META.'}}',
        ));
    }

    #[Test]
    public function itAnswersUnparseableInputWithParseErrorAndNullId(): void
    {
        self::assertSame(
            ['{"jsonrpc":"2.0","id":null,"error":{"code":-32700,'
                .'"message":"parse error: invalid character \'n\' looking for beginning of object key string"}}'],
            $this->serveLines(self::bridge(), null, '{not json'),
        );
    }

    #[Test]
    public function itRefusesABatchAndANonObjectWithInvalidRequestAndNullId(): void
    {
        self::assertSame(
            [
                '{"jsonrpc":"2.0","id":null,"error":{"code":-32600,'
                    .'"message":"invalid request: batch requests are not supported"}}',
                '{"jsonrpc":"2.0","id":null,"error":{"code":-32600,'
                    .'"message":"invalid request: batch requests are not supported"}}',
                '{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"invalid request: not a JSON-RPC object"}}',
                '{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"invalid request: not a JSON-RPC object"}}',
            ],
            $this->serveLines(self::bridge(), null, '[{"jsonrpc":"2.0","id":1,"method":"ping"}]', '[]', '42', 'null'),
        );
    }

    #[Test]
    public function itTreatsAnEmptyObjectAsAnObjectNotABatch(): void
    {
        // json_decode maps both {} and [] to an empty PHP array; framing
        // must still tell them apart. {} has no id: dropped silently.
        self::assertSame([], $this->serveLines(self::bridge(), null, '{}'));
    }

    #[Test]
    public function itAnswersARequestWithoutAMethodWithInvalidRequestAndItsId(): void
    {
        self::assertSame(
            ['{"jsonrpc":"2.0","id":9,"error":{"code":-32600,"message":"invalid request: missing method"}}'],
            $this->serveLines(self::bridge(), null, '{"jsonrpc":"2.0","id":9}'),
        );
    }

    #[Test]
    public function itIgnoresAClientResponseAndLogsIt(): void
    {
        self::assertSame([], $this->serveLines(
            self::bridge(),
            null,
            '{"jsonrpc":"2.0","id":1,"result":{}}',
            '{"jsonrpc":"2.0","id":2,"error":{"code":-1,"message":"x"}}',
        ));
        self::assertStringContainsString('ignored a JSON-RPC response', $this->log);
    }

    #[Test]
    public function itSkipsBlankLinesAcceptsCrlfAndAnUnterminatedLastLine(): void
    {
        $lines = $this->runRaw(
            self::bridge(),
            null,
            "\n   \n{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\r\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"ping\"}",
        );

        self::assertSame([
            '{"jsonrpc":"2.0","id":1,"result":{}}',
            '{"jsonrpc":"2.0","id":2,"result":{}}',
        ], $lines);
    }

    #[Test]
    public function itAnswersRequestsInTheOrderTheyArrive(): void
    {
        $lines = $this->serveLines(
            self::bridge(),
            null,
            '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ping"}}',
            '{"jsonrpc":"2.0","id":1,"method":"ping"}',
            '{"jsonrpc":"2.0","id":3,"method":"tools/list"}',
        );

        self::assertSame([2, 1, 3], array_map(static fn (array $m): mixed => $m['id'], self::decodeAll($lines)));
    }

    #[Test]
    public function itServesTheModernEraFromTheBodyAloneWithNoHeaderLayer(): void
    {
        $lines = $this->serveLines(
            self::bridge(),
            null,
            '{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{'.self::MODERN_META.'}}',
            '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ping",'.self::MODERN_META.'}}',
            '{"jsonrpc":"2.0","id":3,"method":"ping","params":{'.self::MODERN_META.'}}',
        );
        [$discover, $call, $ping] = self::decodeAll($lines);

        self::assertSame('complete', $discover['result']['resultType'] ?? null);
        self::assertSame(['2026-07-28'], $discover['result']['supportedVersions'] ?? null);
        self::assertSame('complete', $call['result']['resultType'] ?? null);
        self::assertFalse($call['result']['isError'] ?? null);
        self::assertStringContainsString('pong', $lines[1]);
        // 2026-07-28 removed ping: a modern ping is an unknown method.
        self::assertSame(-32601, $ping['error']['code'] ?? null);
    }

    #[Test]
    public function itKeepsTheRemainingModernValidation(): void
    {
        $lines = $this->serveLines(
            self::bridge(),
            null,
            '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":'
            .'{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2099-01-01"}}}',
            '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"_meta":'
            .'{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}',
        );
        [$version, $capabilities] = self::decodeAll($lines);

        self::assertSame(-32022, $version['error']['code'] ?? null);
        self::assertSame(-32602, $capabilities['error']['code'] ?? null, 'V3: clientCapabilities is required');
    }

    #[Test]
    public function itRefusesAnAuthRequiredLeafWithoutAVerifier(): void
    {
        $lines = $this->serveLines(
            self::bridge(),
            null,
            '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"secret"}}',
            '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"secret",'.self::MODERN_META.'}}',
        );

        foreach ($lines as $line) {
            self::assertStringContainsString('authentication required', $line);
        }
        self::assertCount(2, $lines);
    }

    #[Test]
    public function itCallsTheVerifierWithAHeaderlessRequest(): void
    {
        $seen = [];
        $mount = new Mount(verifier: static function (Request $r) use (&$seen): Identity {
            $seen[] = $r->headers;

            return new Identity(caller: 'spawner');
        });

        $lines = $this->serveLines(
            self::bridge(),
            $mount,
            '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"secret"}}',
            '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"secret",'.self::MODERN_META.'}}',
        );

        self::assertSame([[], []], $seen, 'stdio fabricates no headers');
        foreach ($lines as $line) {
            self::assertStringContainsString('"isError":false', $line);
        }
    }

    #[Test]
    public function theConfirmationHeaderGateCannotBeSatisfied(): void
    {
        $lines = $this->serveLines(
            self::bridge(),
            null,
            '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"deploy"}}',
            '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"deploy",'.self::MODERN_META.'}}',
        );

        self::assertCount(2, $lines);
        foreach ($lines as $line) {
            self::assertStringContainsString('confirmation required', $line);
        }
    }

    #[Test]
    public function itReusesARequestHandlersBridgeAndMount(): void
    {
        $factory = new Psr17Factory();
        $handler = new RequestHandler(
            self::bridge(),
            new Mount(serverInfo: new ServerInfo('stdio-tool', '9.9.9')),
            $factory,
            $factory,
        );

        $lines = $this->serveLines(
            $handler,
            null,
            '{"jsonrpc":"2.0","id":1,"method":"initialize"}',
            '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{'.self::MODERN_META.'}}',
        );
        [$init, $list] = self::decodeAll($lines);

        self::assertSame(['name' => 'stdio-tool', 'version' => '9.9.9'], $init['result']['serverInfo'] ?? null);
        self::assertSame('complete', $list['result']['resultType'] ?? null);
    }

    #[Test]
    public function itAppliesMountOptionsWhenServingABridge(): void
    {
        $lines = $this->serveLines(
            self::bridge(),
            new Mount(specVersions: [SpecVersion::Modern]),
            '{"jsonrpc":"2.0","id":1,"method":"initialize"}',
        );

        self::assertStringContainsString('supported protocol versions: 2026-07-28', $lines[0] ?? '');
    }

    #[Test]
    public function itRefusesMountOptionsAlongsideARequestHandler(): void
    {
        $factory = new Psr17Factory();
        $handler = new RequestHandler(self::bridge(), new Mount(), $factory, $factory);

        $this->expectException(\InvalidArgumentException::class);

        StdioServer::serve($handler, new Mount(), self::stream(''), self::stream(''), self::stream(''));
    }

    #[Test]
    public function itAnswersInternalErrorWhenDispatchThrowsAndLogsTheCause(): void
    {
        $lines = $this->serveLines(
            self::bridge(),
            null,
            '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"boom"}}',
            '{"jsonrpc":"2.0","id":5,"method":"ping"}',
        );

        self::assertSame([
            '{"jsonrpc":"2.0","id":4,"error":{"code":-32603,"message":"internal error"}}',
            '{"jsonrpc":"2.0","id":5,"result":{}}',
        ], $lines);
        self::assertStringContainsString('kaboom', $this->log);
    }

    #[Test]
    public function itReturnsAtEndOfInputWithNothingWritten(): void
    {
        $output = self::stream('');
        $log = self::stream('');

        StdioServer::serve(self::bridge(), null, self::stream(''), $output, $log);

        rewind($output);
        self::assertSame('', stream_get_contents($output));
    }

    /**
     * Writes each message as one line, serves until end of input, and
     * returns every output line.
     *
     * @return list<string>
     */
    private function serveLines(Bridge|RequestHandler $target, ?Mount $mount, string ...$messages): array
    {
        return $this->runRaw($target, $mount, implode('', array_map(static fn (string $m): string => $m."\n", $messages)));
    }

    /** @return list<string> */
    private function runRaw(Bridge|RequestHandler $target, ?Mount $mount, string $input): array
    {
        $output = self::stream('');
        $log = self::stream('');

        StdioServer::serve($target, $mount, self::stream($input), $output, $log);

        rewind($output);
        rewind($log);
        $this->log = (string) stream_get_contents($log);
        $out = (string) stream_get_contents($output);

        if ('' !== $out) {
            self::assertStringEndsWith("\n", $out, 'every reply is a whole line');
        }

        return array_values(array_filter(explode("\n", $out), static fn (string $l): bool => '' !== $l));
    }

    /** @return resource */
    private static function stream(string $content)
    {
        $stream = fopen('php://memory', 'w+');
        self::assertIsResource($stream);
        fwrite($stream, $content);
        rewind($stream);

        return $stream;
    }

    /**
     * @param list<string> $lines
     *
     * @return list<array<string, mixed>>
     */
    private static function decodeAll(array $lines): array
    {
        return array_map(static function (string $l): array {
            $decoded = json_decode($l, true, 512, \JSON_THROW_ON_ERROR);
            self::assertIsArray($decoded);

            /** @var array<string, mixed> $decoded */
            return $decoded;
        }, $lines);
    }

    /** The legacy lock tree plus a leaf whose runner throws. */
    private static function bridge(): Bridge
    {
        $root = LockTrees::legacy()->addCommand(new Command(
            name: 'boom',
            description: 'Throws',
            annotations: ['kit/side-effect' => 'read'],
            runner: static fn (): Result => throw new \RuntimeException('kaboom'),
        ));

        return new Bridge($root, Policy::default());
    }
}
